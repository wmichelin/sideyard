package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/wmichelin/clubdue/internal/photostore"
)

type AzurePhotoConfig struct {
	AccountURL, TenantID, ClientID, ClientSecret string
}

func loadAzurePhotoConfig(getenv func(string) string) (AzurePhotoConfig, error) {
	endpoint := strings.TrimSpace(getenv("AZURE_BLOB_ACCOUNT_URL"))
	if endpoint == "" {
		return AzurePhotoConfig{}, nil
	}
	c := AzurePhotoConfig{AccountURL: endpoint, TenantID: strings.TrimSpace(getenv("AZURE_TENANT_ID")), ClientID: strings.TrimSpace(getenv("AZURE_CLIENT_ID")), ClientSecret: getenv("AZURE_CLIENT_SECRET")}
	if err := photostore.ValidateEndpoint(c.AccountURL); err != nil {
		return AzurePhotoConfig{}, err
	}
	if c.TenantID == "" || c.ClientID == "" || c.ClientSecret == "" {
		return AzurePhotoConfig{}, errors.New("Azure photo storage requires AZURE_TENANT_ID, AZURE_CLIENT_ID and AZURE_CLIENT_SECRET")
	}
	return c, nil
}

type photoObjectStorage interface {
	Put(context.Context, string, string, []byte) (photostore.Reference, error)
	Get(context.Context, photostore.Reference) ([]byte, error)
}

func newAzurePhotoStorage(c AzurePhotoConfig) (photoObjectStorage, error) {
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	credential, err := azidentity.NewClientSecretCredential(c.TenantID, c.ClientID, c.ClientSecret, &azidentity.ClientSecretCredentialOptions{ClientOptions: azcore.ClientOptions{Transport: client}})
	if err != nil {
		return nil, errors.New("initialize Azure photo credentials")
	}
	return photostore.NewAzure(c.AccountURL, "photos", credential, client)
}

// A bounded command makes interrupted handoffs recoverable from the CLI or a
// scheduled maintenance window without a polling worker holding SQL awake.
func runSyncPhotos(config Config) error {
	if config.DatabaseURL == "" || config.AzurePhotos.AccountURL == "" {
		return errors.New("sync-photos requires a database and configured Azure photo storage")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	db, err := openDB(ctx, config.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	objects, err := newAzurePhotoStorage(config.AzurePhotos)
	if err != nil {
		return err
	}
	store := &Store{db: db, photoObjects: objects}
	total := 0
	for batch := 0; batch < 10; batch++ {
		n, err := store.SyncPhotoObjects(ctx, 100)
		total += n
		if err != nil {
			return fmt.Errorf("photo sync stopped after %d verified handoffs: %w", total, err)
		}
		if n < 100 {
			log.Printf("photo sync complete: %d verified handoffs; database recovery copies retained", total)
			return nil
		}
	}
	return errors.New("photo sync reached its 1000-photo batch limit; run sync-photos again")
}

// syncPropertyPhotoObject makes the handoff durable before contacting Azure.
// Inline bytes are a recovery copy during migration and let a retry resume even
// when the first upload's HTTP response or the final SQL commit was lost.
func (s *Store) syncPropertyPhotoObject(ctx context.Context, shopID, photoID string) error {
	if s.photoObjects == nil {
		return nil
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var photo normalizedPhoto
	var key, version, hash string
	err = tx.QueryRow(ctx, `SELECT content_type,image_data,COALESCE(object_key,''),COALESCE(object_version,''),content_hash FROM property_photos WHERE shop_id=$1 AND id=$2 AND deleted_at IS NULL FOR UPDATE`, shopID, photoID).Scan(&photo.ContentType, &photo.Data, &key, &version, &hash)
	if err != nil {
		return err
	}
	if version != "" {
		return nil
	}
	if len(photo.Data) < 1 || len(photo.Data) > photoStoredLimit || fmt.Sprintf("%x", sha256.Sum256(photo.Data)) != hash {
		return errors.New("photo recovery copy failed integrity verification")
	}
	if key == "" {
		key = fmt.Sprintf("%x", sha256.Sum256([]byte("sideyard-photo/"+shopID+"/"+photoID)))
		if _, err := tx.Exec(ctx, `UPDATE property_photos SET object_key=$3 WHERE shop_id=$1 AND id=$2`, shopID, photoID, key); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO property_photo_objects(object_key,shop_id,photo_id,content_hash,content_type,byte_length) VALUES ($1,$2,$3,$4,$5,$6)`, key, shopID, photoID, hash, photo.ContentType, len(photo.Data)); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	ref, err := s.photoObjects.Put(ctx, key, photo.ContentType, photo.Data)
	if err != nil {
		return err
	}
	if ref.Key != key || ref.VersionID == "" || ref.SHA256 != hash || ref.Size != len(photo.Data) || ref.ContentType != photo.ContentType {
		return errors.New("photo storage returned an invalid reference")
	}
	// Record a late upload even if the photo was deleted during the network call.
	// This inventory survives cascades and allows future version-aware collection.
	// Commit inventory independently: deletion locks photo then inventory, so
	// holding inventory while acquiring a photo lock would invert lock order.
	tag, err := s.db.Pool.Exec(ctx, `UPDATE property_photo_objects SET version_id=$2 WHERE object_key=$1 AND (version_id IS NULL OR version_id=$2)`, key, ref.VersionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errPhotoConflict
	}
	tag, err = s.db.Pool.Exec(ctx, `UPDATE property_photos SET object_version=$4,object_pending=false WHERE shop_id=$1 AND id=$2 AND object_key=$3 AND deleted_at IS NULL AND (object_version IS NULL OR object_version=$4)`, shopID, photoID, key, ref.VersionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errPhotoConflict
	}
	return nil
}

// SyncPhotoObjects is an operator/batched-maintenance operation. It also resumes
// interrupted uploads; it never visits a deleted photo or invents a new key.
func (s *Store) SyncPhotoObjects(ctx context.Context, limit int) (int, error) {
	if s.photoObjects == nil {
		return 0, errors.New("Azure photo storage is not configured")
	}
	if limit < 1 || limit > 100 {
		return 0, errors.New("photo sync batch must be between 1 and 100")
	}
	rows, err := s.db.Pool.Query(ctx, `SELECT shop_id,id FROM property_photos WHERE deleted_at IS NULL AND object_version IS NULL ORDER BY recorded_at,id LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	type pending struct{ shop, photo string }
	var batch []pending
	for rows.Next() {
		var item pending
		if err := rows.Scan(&item.shop, &item.photo); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for i, item := range batch {
		if err := s.syncPropertyPhotoObject(ctx, item.shop, item.photo); err != nil {
			return i, err
		}
	}
	return len(batch), nil
}

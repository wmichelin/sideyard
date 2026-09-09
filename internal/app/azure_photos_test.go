package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/wmichelin/clubdue/internal/photostore"
)

type fakePhotoObjects struct {
	mu         sync.Mutex
	objects    map[string][]byte
	putErr     error
	getErr     error
	puts, gets int
	beforePut  func()
}

func (f *fakePhotoObjects) Put(_ context.Context, key, mime string, data []byte) (photostore.Reference, error) {
	if f.beforePut != nil {
		f.beforePut()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	if f.putErr != nil {
		return photostore.Reference{}, f.putErr
	}
	if f.objects == nil {
		f.objects = make(map[string][]byte)
	}
	if old, ok := f.objects[key]; ok && !bytes.Equal(old, data) {
		return photostore.Reference{}, errors.New("immutable object changed")
	}
	f.objects[key] = bytes.Clone(data)
	return photostore.Reference{Key: key, VersionID: "saved-version", SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), ContentType: mime, Size: len(data)}, nil
}

func (f *fakePhotoObjects) Get(_ context.Context, ref photostore.Reference) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.getErr != nil {
		return nil, f.getErr
	}
	data, ok := f.objects[ref.Key]
	if !ok || ref.VersionID != "saved-version" || ref.Size != len(data) || ref.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
		return nil, errors.New("wrong version reference")
	}
	return bytes.Clone(data), nil
}

func TestAzurePhotoHandoffRetriesAndPrivateReads(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	shop := createTestShop(t, ctx, db, fmt.Sprintf("azure-photo-%d", time.Now().UnixNano()))
	other := createTestShop(t, ctx, db, fmt.Sprintf("azure-other-%d", time.Now().UnixNano()))
	customer := createTestCustomer(t, ctx, db, shop, "Photo fixture")
	property := createTestProperty(t, ctx, db, shop, customer, "123 Fixture St")
	photo, err := normalizePhoto(photoFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	objects := &fakePhotoObjects{putErr: errors.New("storage unavailable")}
	s := &Store{db: db, photoObjects: objects}
	key := strings.Repeat("a", 32)
	id, err := s.AddPropertyPhoto(ctx, shop, property, key, "Before", "before", "owner", photo)
	if err == nil || id == "" {
		t.Fatalf("upload did not retain a resumable failure: %s %v", id, err)
	}
	listed, count, used, err := s.ListPropertyPhotos(ctx, shop, property)
	if err != nil || len(listed) != 0 || count != 1 || used != len(photo.Data) {
		t.Fatalf("pending upload visibility/quota: %d %d %d %v", len(listed), count, used, err)
	}
	if _, err := s.GetPropertyPhoto(ctx, shop, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("pending upload exposed: %v", err)
	}
	objects.putErr = nil
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			id, err := s.AddPropertyPhoto(ctx, shop, property, key, "Before", "before", "owner", photo)
			results <- result{id, err}
		}()
	}
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil || got.id != id {
			t.Fatalf("retry: %+v", got)
		}
	}
	if len(objects.objects) != 1 {
		t.Fatal("retry created duplicate objects")
	}
	if _, err := s.AddPropertyPhoto(ctx, shop, property, key, "Changed", "before", "owner", photo); !errors.Is(err, errPhotoConflict) {
		t.Fatalf("changed retry: %v", err)
	}
	got, err := s.GetPropertyPhoto(ctx, shop, id)
	if err != nil || !bytes.Equal(got.Data, photo.Data) || objects.gets != 1 {
		t.Fatalf("Azure read: %v", err)
	}
	if _, err := s.GetPropertyPhoto(ctx, other, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign read: %v", err)
	}
	if _, err := s.DeletePropertyPhoto(ctx, other, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign delete: %v", err)
	}
	if objects.gets != 1 {
		t.Fatal("foreign access reached object storage")
	}
	objects.getErr = errors.New("storage unavailable")
	if _, err := s.GetPropertyPhoto(ctx, shop, id); err == nil {
		t.Fatal("Azure outage silently reported success")
	}
	// Explicitly disabling Azure is the tested recovery/rollback mode.
	recovery := &Store{db: db}
	got, err = recovery.GetPropertyPhoto(ctx, shop, id)
	if err != nil || !bytes.Equal(got.Data, photo.Data) {
		t.Fatal("inline recovery copy unavailable")
	}
}

func TestAzurePhotoBackfillPreservesExistingPhotos(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	shop := createTestShop(t, ctx, db, fmt.Sprintf("azure-backfill-%d", time.Now().UnixNano()))
	customer := createTestCustomer(t, ctx, db, shop, "Photo fixture")
	property := createTestProperty(t, ctx, db, shop, customer, "125 Fixture St")
	photo, err := normalizePhoto(photoFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{db: db}
	id, err := s.AddPropertyPhoto(ctx, shop, property, strings.Repeat("b", 32), "Old photo", "general", "owner", photo)
	if err != nil {
		t.Fatal(err)
	}
	objects := &fakePhotoObjects{putErr: errors.New("storage unavailable")}
	s.photoObjects = objects
	if err := s.syncPropertyPhotoObject(ctx, shop, id); err == nil {
		t.Fatal("expected failed backfill")
	}
	got, err := s.GetPropertyPhoto(ctx, shop, id)
	if err != nil || !bytes.Equal(got.Data, photo.Data) {
		t.Fatal("failed backfill hid existing photo")
	}
	objects.putErr = nil
	if err := s.syncPropertyPhotoObject(ctx, shop, id); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetPropertyPhoto(ctx, shop, id)
	if err != nil || !bytes.Equal(got.Data, photo.Data) || objects.gets != 1 {
		t.Fatal("backfill did not switch to exact Azure version")
	}
}

func TestAzurePhotoDeleteDuringUploadRetainsVersionInventory(t *testing.T) {
	for _, cascade := range []bool{false, true} {
		t.Run(fmt.Sprintf("customer_cascade_%v", cascade), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			db, err := openDB(ctx, integrationDatabaseURL(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(db.Close)
			shop := createTestShop(t, ctx, db, fmt.Sprintf("azure-delete-%d", time.Now().UnixNano()))
			customer := createTestCustomer(t, ctx, db, shop, "Photo fixture")
			property := createTestProperty(t, ctx, db, shop, customer, "127 Fixture St")
			photo, err := normalizePhoto(photoFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			started, release := make(chan struct{}), make(chan struct{})
			objects := &fakePhotoObjects{beforePut: func() { close(started); <-release }}
			s := &Store{db: db, photoObjects: objects}
			finished := make(chan error, 1)
			go func() {
				_, err := s.AddPropertyPhoto(ctx, shop, property, strings.Repeat("c", 32), "Delete race", "general", "owner", photo)
				finished <- err
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("upload never started")
			}
			var id, key string
			if err := db.Pool.QueryRow(ctx, `SELECT id,object_key FROM property_photos WHERE shop_id=$1`, shop).Scan(&id, &key); err != nil {
				close(release)
				t.Fatal(err)
			}
			if cascade {
				_, err = db.Pool.Exec(ctx, `DELETE FROM customers WHERE shop_id=$1 AND id=$2`, shop, customer)
			} else {
				_, err = s.DeletePropertyPhoto(ctx, shop, id)
			}
			close(release)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-finished; !errors.Is(err, errPhotoConflict) {
				t.Fatalf("late completion: %v", err)
			}
			if _, err := s.GetPropertyPhoto(ctx, shop, id); !errors.Is(err, pgx.ErrNoRows) {
				t.Fatal("late upload resurrected photo")
			}
			var retired bool
			var version string
			if err := db.Pool.QueryRow(ctx, `SELECT retired_at IS NOT NULL,COALESCE(version_id,'') FROM property_photo_objects WHERE object_key=$1`, key).Scan(&retired, &version); err != nil || !retired || version != "saved-version" {
				t.Fatalf("late version inventory lost: %v %s %v", retired, version, err)
			}
		})
	}
}

func TestAzurePhotoConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]string
		valid  bool
	}{
		{"disabled", nil, true},
		{"missing credentials", map[string]string{"AZURE_BLOB_ACCOUNT_URL": "https://sideyardtest.blob.core.windows.net"}, false},
		{"private endpoint", map[string]string{"AZURE_BLOB_ACCOUNT_URL": "http://127.0.0.1", "AZURE_TENANT_ID": "tenant", "AZURE_CLIENT_ID": "client", "AZURE_CLIENT_SECRET": "secret"}, false},
		{"configured", map[string]string{"AZURE_BLOB_ACCOUNT_URL": "https://sideyardtest.blob.core.windows.net", "AZURE_TENANT_ID": "tenant", "AZURE_CLIENT_ID": "client", "AZURE_CLIENT_SECRET": "secret"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadAzurePhotoConfig(func(key string) string { return tc.values[key] })
			if (err == nil) != tc.valid {
				t.Fatalf("configuration: %v", err)
			}
		})
	}
}

// Explicit opt-in: ordinary CI uses fake Blob transport and real local Postgres.
// Run only with a disposable, empty test DB and the staging photo identity.
func TestAzurePhotoLiveHandoff(t *testing.T) {
	if os.Getenv("SIDEYARD_AZURE_LIVE_TEST") != "1" {
		t.Skip("explicit live staging Blob test opt-in required")
	}
	c, err := loadAzurePhotoConfig(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.AccountURL, "sideyardstg") {
		t.Fatal("live test requires staging storage")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	objects, err := newAzurePhotoStorage(c)
	if err != nil {
		t.Fatal(err)
	}
	shop := createTestShop(t, ctx, db, fmt.Sprintf("azure-live-%d", time.Now().UnixNano()))
	customer := createTestCustomer(t, ctx, db, shop, "Synthetic Azure verification")
	property := createTestProperty(t, ctx, db, shop, customer, "Synthetic test location")
	photo, err := normalizePhoto(photoFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{db: db}
	id, err := s.AddPropertyPhoto(ctx, shop, property, strings.Repeat("d", 32), "Synthetic verification", "general", "test", photo)
	if err != nil {
		t.Fatal(err)
	}
	s.photoObjects = objects
	if n, err := s.SyncPhotoObjects(ctx, 100); err != nil || n != 1 {
		t.Fatalf("live sync: %d %v", n, err)
	}
	if n, err := s.SyncPhotoObjects(ctx, 100); err != nil || n != 0 {
		t.Fatalf("repeat sync: %d %v", n, err)
	}
	got, err := s.GetPropertyPhoto(ctx, shop, id)
	if err != nil || !bytes.Equal(got.Data, photo.Data) {
		t.Fatalf("live exact-version read: %v", err)
	}
	if _, err := s.DeletePropertyPhoto(ctx, shop, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPropertyPhoto(ctx, shop, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("deleted live test image is readable")
	}
	t.Log("Azure staging handoff, exact version, idempotent CLI batch and deletion verified; synthetic retired object retained for recovery")
}

package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
	"github.com/wmichelin/clubdue/internal/photostore"
)

var errPhotoConflict = errors.New("This upload was already used or deleted. Reload the photo page before adding a different photo.")
var errPhotoQuota = errors.New("Your business photo storage is full (100 photos or 50 MB). Delete unneeded photos before adding another.")
var errPhotoInput = errors.New("Choose a photo category and a caption of at most 500 bytes.")

type propertyPhoto struct {
	ID, PropertyID, Caption, Category, RecordedBy string
	RecordedAt                                    time.Time
}

var photoCategories = []struct{ ID, Name string }{
	{"before", "Before"}, {"after", "After"}, {"damage", "Damage"}, {"access", "Access"}, {"general", "General"},
}

func validPhotoFields(key, caption, category string) bool {
	if !noteSubmissionKey.MatchString(key) || len(caption) > 500 || !validStoredText(caption) {
		return false
	}
	for _, c := range photoCategories {
		if c.ID == category {
			return true
		}
	}
	return false
}

func (s *Store) AddPropertyPhoto(ctx context.Context, shopID, propertyID, key, caption, category, author string, photo normalizedPhoto) (string, error) {
	id, err := s.addPropertyPhotoRecord(ctx, shopID, propertyID, key, caption, category, author, photo)
	if err != nil {
		return "", err
	}
	return id, s.syncPropertyPhotoObject(ctx, shopID, id)
}

func (s *Store) addPropertyPhotoRecord(ctx context.Context, shopID, propertyID, key, caption, category, author string, photo normalizedPhoto) (string, error) {
	if err := validateRecordIDs(shopID, propertyID); err != nil {
		return "", err
	}
	if !validStoredText(author) {
		return "", errPhotoInput
	}
	if !validPhotoFields(key, caption, category) {
		return "", errPhotoInput
	}
	if len(photo.Data) == 0 || len(photo.Data) > photoStoredLimit || (photo.ContentType != "image/jpeg" && photo.ContentType != "image/png") {
		return "", errPhotoImage
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	// The shop lock serializes quota checks with other uploads and deletions.
	var locked string
	if err := tx.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT id FROM shops WHERE id=$1 FOR UPDATE`, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.shops WITH(UPDLOCK,HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`), shopID).Scan(&locked); err != nil {
		return "", err
	}
	if err := tx.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT id FROM properties WHERE shop_id=$1 AND id=$2`, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.properties WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shopID, propertyID).Scan(&locked); err != nil {
		return "", err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(photo.Data))
	var id, previousProperty, previousHash, previousCaption, previousCategory, previousAuthor string
	var deleted bool
	err = tx.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT id,property_id,content_hash,caption,category,recorded_by,deleted_at IS NOT NULL FROM property_photos WHERE shop_id=$1 AND submission_key=$2`, `SELECT LOWER(CONVERT(char(36),id)),LOWER(CONVERT(char(36),property_id)),content_hash,caption,category,recorded_by,CONVERT(bit,CASE WHEN deleted_at IS NULL THEN 0 ELSE 1 END) FROM dbo.property_photos WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND submission_key=@p2 AND DATALENGTH(submission_key)=DATALENGTH(@p2)`), shopID, key).Scan(&id, &previousProperty, &previousHash, &previousCaption, &previousCategory, &previousAuthor, &deleted)
	if err == nil {
		if deleted || !strings.EqualFold(previousProperty, propertyID) || previousHash != hash || previousCaption != caption || previousCategory != category || previousAuthor != author {
			return "", errPhotoConflict
		}
		return id, nil
	}
	if !errors.Is(err, database.ErrNoRows) {
		return "", err
	}
	var count, used int
	if err := tx.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT count(*),COALESCE(sum(octet_length(image_data)),0) FROM property_photos WHERE shop_id=$1 AND deleted_at IS NULL`, `SELECT COUNT(*),COALESCE(SUM(DATALENGTH(image_data)),0) FROM dbo.property_photos WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND deleted_at IS NULL`), shopID).Scan(&count, &used); err != nil {
		return "", err
	}
	if count >= photoShopCount || used+len(photo.Data) > photoShopBytes {
		return "", errPhotoQuota
	}
	err = tx.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `INSERT INTO property_photos(shop_id,property_id,submission_key,content_hash,caption,category,content_type,image_data,recorded_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, `DECLARE @created TABLE(id uniqueidentifier);
INSERT INTO dbo.property_photos(shop_id,property_id,submission_key,content_hash,caption,category,content_type,image_data,recorded_by) OUTPUT inserted.id INTO @created VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),@p3,@p4,@p5,@p6,@p7,@p8,@p9);
SELECT LOWER(CONVERT(char(36),id)) FROM @created;`), shopID, propertyID, key, hash, caption, category, photo.ContentType, photo.Data, author).Scan(&id)
	if err != nil {
		return "", err
	}
	if s.photoObjects != nil {
		objectKey := fmt.Sprintf("%x", sha256.Sum256([]byte("sideyard-photo/"+shopID+"/"+id)))
		if _, err := tx.Exec(ctx, database.Statement(s.db.Pool.Dialect(), `UPDATE property_photos SET object_key=$3,object_pending=true WHERE shop_id=$1 AND id=$2`, `UPDATE dbo.property_photos SET object_key=@p3,object_pending=1 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shopID, id, objectKey); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, database.Statement(s.db.Pool.Dialect(), `INSERT INTO property_photo_objects(object_key,shop_id,photo_id,content_hash,content_type,byte_length) VALUES ($1,$2,$3,$4,$5,$6)`, `INSERT INTO dbo.property_photo_objects(object_key,shop_id,photo_id,content_hash,content_type,byte_length) VALUES(@p1,CONVERT(uniqueidentifier,@p2),CONVERT(uniqueidentifier,@p3),@p4,@p5,@p6)`), objectKey, shopID, id, hash, photo.ContentType, len(photo.Data)); err != nil {
			return "", err
		}
	}
	return id, tx.Commit(ctx)
}

func (s *Store) ListPropertyPhotos(ctx context.Context, shopID, propertyID string) ([]propertyPhoto, int, int, error) {
	if err := validateRecordIDs(shopID, propertyID); err != nil {
		return nil, 0, 0, err
	}
	var count, used int
	if err := s.db.Pool.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT count(*),COALESCE(sum(octet_length(image_data)),0) FROM property_photos WHERE shop_id=$1 AND deleted_at IS NULL`, `SELECT COUNT(*),COALESCE(SUM(DATALENGTH(image_data)),0) FROM dbo.property_photos WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND deleted_at IS NULL`), shopID).Scan(&count, &used); err != nil {
		return nil, 0, 0, err
	}
	rows, err := s.db.Pool.Query(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT id,property_id,caption,category,recorded_by,recorded_at FROM property_photos WHERE shop_id=$1 AND property_id=$2 AND deleted_at IS NULL AND (NOT object_pending OR $3) ORDER BY recorded_at DESC,id LIMIT 100`, `SELECT TOP(100) LOWER(CONVERT(char(36),id)),LOWER(CONVERT(char(36),property_id)),caption,category,recorded_by,recorded_at FROM dbo.property_photos WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND property_id=CONVERT(uniqueidentifier,@p2) AND deleted_at IS NULL AND (object_pending=0 OR @p3=1) ORDER BY recorded_at DESC,CONVERT(char(36),id) COLLATE Latin1_General_100_BIN2`), shopID, propertyID, s.photoObjects == nil)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	var photos []propertyPhoto
	for rows.Next() {
		var p propertyPhoto
		if err := rows.Scan(&p.ID, &p.PropertyID, &p.Caption, &p.Category, &p.RecordedBy, &p.RecordedAt); err != nil {
			return nil, 0, 0, err
		}
		photos = append(photos, p)
	}
	return photos, count, used, rows.Err()
}

func (s *Store) GetPropertyPhoto(ctx context.Context, shopID, id string) (normalizedPhoto, error) {
	if err := validateRecordIDs(shopID, id); err != nil {
		return normalizedPhoto{}, err
	}
	var photo normalizedPhoto
	var ref photostore.Reference
	err := s.db.Pool.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT content_type,image_data,COALESCE(object_key,''),COALESCE(object_version,''),content_hash FROM property_photos WHERE shop_id=$1 AND id=$2 AND deleted_at IS NULL AND (NOT object_pending OR $3)`, `SELECT content_type,image_data,COALESCE(object_key,N''),COALESCE(object_version,N''),content_hash FROM dbo.property_photos WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2) AND deleted_at IS NULL AND (object_pending=0 OR @p3=1)`), shopID, id, s.photoObjects == nil).Scan(&photo.ContentType, &photo.Data, &ref.Key, &ref.VersionID, &ref.SHA256)
	if err != nil {
		return normalizedPhoto{}, err
	}
	if ref.VersionID != "" && s.photoObjects != nil {
		ref.ContentType, ref.Size = photo.ContentType, len(photo.Data)
		photo.Data, err = s.photoObjects.Get(ctx, ref)
	}
	return photo, err
}

func (s *Store) DeletePropertyPhoto(ctx context.Context, shopID, id string) (string, error) {
	if err := validateRecordIDs(shopID, id); err != nil {
		return "", err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var locked, propertyID string
	if err := tx.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT id FROM shops WHERE id=$1 FOR UPDATE`, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.shops WITH(UPDLOCK,HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`), shopID).Scan(&locked); err != nil {
		return "", err
	}
	err = tx.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `UPDATE property_photos SET image_data=NULL,caption='',recorded_by='',deleted_at=COALESCE(deleted_at,now()) WHERE shop_id=$1 AND id=$2 RETURNING property_id`, `DECLARE @deleted TABLE(property_id uniqueidentifier);
UPDATE dbo.property_photos SET image_data=NULL,caption=N'',recorded_by=N'',deleted_at=COALESCE(deleted_at,SYSUTCDATETIME()) OUTPUT inserted.property_id INTO @deleted WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2);
SELECT LOWER(CONVERT(char(36),property_id)) FROM @deleted;`), shopID, id).Scan(&propertyID)
	if err != nil {
		return "", err
	}
	return propertyID, tx.Commit(ctx)
}

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

func TestPropertyPhotosIsolationRetriesQuotasAndDeletion(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	s := &Store{db: db}
	stamp := time.Now().Format("150405.000000000")
	shop := createTestShop(t, ctx, db, "photos-"+stamp)
	other := createTestShop(t, ctx, db, "photos-other-"+stamp)
	customer := createTestCustomer(t, ctx, db, shop, "Photo customer")
	property := createTestProperty(t, ctx, db, shop, customer, "12 Photo St")
	photo, err := normalizePhoto(photoFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 32)
	if _, err := s.AddPropertyPhoto(ctx, other, property, key, "", "before", "owner", photo); !errors.Is(err, database.ErrNoRows) {
		t.Fatalf("foreign insert: %v", err)
	}
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			id, err := s.AddPropertyPhoto(ctx, shop, property, key, "Gate <script>private</script>", "before", "owner", photo)
			results <- result{id, err}
		}()
	}
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.id != second.id {
		t.Fatalf("retry dedup: %+v %+v", first, second)
	}
	id := first.id
	if _, err := s.AddPropertyPhoto(ctx, shop, property, key, "Different caption", "before", "owner", photo); !errors.Is(err, errPhotoConflict) {
		t.Fatalf("reused key: %v", err)
	}
	if _, err := s.GetPropertyPhoto(ctx, other, id); !errors.Is(err, database.ErrNoRows) {
		t.Fatalf("foreign read: %v", err)
	}
	if _, err := s.DeletePropertyPhoto(ctx, other, id); !errors.Is(err, database.ErrNoRows) {
		t.Fatalf("foreign deletion: %v", err)
	}
	got, err := s.GetPropertyPhoto(ctx, shop, id)
	if err != nil || !bytes.Equal(got.Data, photo.Data) {
		t.Fatal("stored bytes changed")
	}
	photos, count, used, err := s.ListPropertyPhotos(ctx, shop, property)
	if err != nil || len(photos) != 1 || count != 1 || used != len(photo.Data) || photos[0].RecordedBy != "owner" {
		t.Fatal("photo list/usage incorrect")
	}
	// Leave one count slot; concurrent uploads cannot both consume it.
	_, err = db.Pool.Exec(ctx, `INSERT INTO property_photos(shop_id,property_id,submission_key,content_hash,caption,category,content_type,image_data,recorded_by) SELECT $1,$2,md5(i::text),repeat('b',64),'','general','image/png',$3,'quota fixture' FROM generate_series(1,98) i`, shop, property, photo.Data)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		go func(i int) {
			id, err := s.AddPropertyPhoto(ctx, shop, property, fmt.Sprintf("%032x", 1000+i), "", "general", "owner", photo)
			results <- result{id, err}
		}(i)
	}
	wins, full := 0, 0
	for i := 0; i < 2; i++ {
		r := <-results
		switch {
		case r.err == nil:
			wins++
		case errors.Is(r.err, errPhotoQuota):
			full++
		default:
			t.Fatal(r.err)
		}
	}
	if wins != 1 || full != 1 {
		t.Fatalf("quota race: %d saved %d full", wins, full)
	}
	// Retrying an already-saved upload still works even when storage is full.
	if retried, err := s.AddPropertyPhoto(ctx, shop, property, key, "Gate <script>private</script>", "before", "owner", photo); err != nil || retried != id {
		t.Fatal("full storage broke idempotent retry")
	}
	if deletedProperty, err := s.DeletePropertyPhoto(ctx, shop, id); err != nil || deletedProperty != property {
		t.Fatalf("delete: %s %v", deletedProperty, err)
	}
	if _, err := s.GetPropertyPhoto(ctx, shop, id); !errors.Is(err, database.ErrNoRows) {
		t.Fatal("deleted photo still readable")
	}
	if _, err := s.AddPropertyPhoto(ctx, shop, property, key, "Gate <script>private</script>", "before", "owner", photo); !errors.Is(err, errPhotoConflict) {
		t.Fatal("retry resurrected deleted photo")
	}
	var empty bool
	if err := db.Pool.QueryRow(ctx, `SELECT image_data IS NULL AND caption='' AND recorded_by='' FROM property_photos WHERE id=$1`, id).Scan(&empty); err != nil || !empty {
		t.Fatal("deleted content retained in live row")
	}
	if _, err := s.DeletePropertyPhoto(ctx, shop, id); err != nil {
		t.Fatal("deletion retry failed")
	}
	if _, err := s.AddPropertyPhoto(ctx, shop, property, strings.Repeat("c", 32), "", "general", "owner", photo); err != nil {
		t.Fatal("delete did not release quota")
	}
	// Byte quota is independent of the count quota; use a separate tenant fixture.
	otherCustomer := createTestCustomer(t, ctx, db, other, "Byte quota customer")
	otherProperty := createTestProperty(t, ctx, db, other, otherCustomer, "14 Photo St")
	_, err = db.Pool.Exec(ctx, `INSERT INTO property_photos(shop_id,property_id,submission_key,content_hash,caption,category,content_type,image_data,recorded_by) SELECT $1,$2,md5(i::text),repeat('b',64),'','general','image/png',decode(repeat('00',4194304),'hex'),'quota fixture' FROM generate_series(1,12) i`, other, otherProperty)
	if err != nil {
		t.Fatal(err)
	}
	large := normalizedPhoto{ContentType: "image/png", Data: make([]byte, 3<<20)}
	if _, err := s.AddPropertyPhoto(ctx, other, otherProperty, key, "", "general", "owner", large); !errors.Is(err, errPhotoQuota) {
		t.Fatalf("byte quota: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO property_photos(shop_id,property_id,submission_key,content_hash,caption,category,content_type,image_data,recorded_by) VALUES ($1,$2,repeat('f',32),repeat('a',64),'','general','image/png',$3,'owner')`, other, property, photo.Data); err == nil {
		t.Fatal("database accepted foreign property")
	}
}

func TestPropertyPhotoHandlersKeepImagesPrivate(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	s := &Store{db: db}
	stamp := time.Now().Format("150405.000000000")
	shop := createTestShop(t, ctx, db, "photo-http-"+stamp)
	other := createTestShop(t, ctx, db, "photo-http-other-"+stamp)
	customer := createTestCustomer(t, ctx, db, shop, "Photo customer")
	property := createTestProperty(t, ctx, db, shop, customer, "12 Photo St")
	server := &Server{store: s, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html"))}
	key := strings.Repeat("a", 32)
	upload := func(tenant, role, caption string, data []byte, demo bool) *httptest.ResponseRecorder {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		for k, v := range map[string]string{"photo_key": key, "caption": caption, "category": "damage"} {
			if err := form.WriteField(k, v); err != nil {
				t.Fatal(err)
			}
		}
		file, err := form.CreateFormFile("photo", "../../private.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/properties/"+property+"/photos", &body)
		r.Header.Set("Content-Type", form.FormDataContentType())
		r.SetPathValue("id", property)
		r = r.WithContext(contextWithUser(ctx, &User{ShopID: tenant, Role: role, Email: "owner@example.test", IsDemo: demo}))
		w := httptest.NewRecorder()
		server.handlePropertyPhotos(w, r)
		return w
	}
	for _, tc := range []struct {
		shop, role string
		demo       bool
		status     int
	}{{other, "owner", false, 404}, {shop, "staff", false, 403}, {shop, "owner", true, 403}} {
		if w := upload(tc.shop, tc.role, "Private", photoFixture(t), tc.demo); w.Code != tc.status {
			t.Fatalf("unauthorized upload: %d", w.Code)
		}
	}
	w := upload(shop, "owner", "Keep <script>caption</script>", []byte("HEIC unsupported"), false)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "Keep &lt;script&gt;caption&lt;/script&gt;") {
		t.Fatalf("invalid upload lost caption: %d %s", w.Code, w.Body.String())
	}
	// A disconnected/truncated multipart body must not persist partial data.
	interrupted := httptest.NewRequest("POST", "/properties/"+property+"/photos", strings.NewReader("--cut\r\nContent-Disposition: form-data; name=\"photo\"; filename=\"photo.png\"\r\n\r\npartial"))
	interrupted.Header.Set("Content-Type", "multipart/form-data; boundary=cut")
	interrupted.SetPathValue("id", property)
	interrupted = interrupted.WithContext(contextWithUser(ctx, &User{ShopID: shop, Role: "owner"}))
	w = httptest.NewRecorder()
	server.handlePropertyPhotos(w, interrupted)
	if w.Code != 400 {
		t.Fatalf("interrupted upload: %d", w.Code)
	}
	if rows, _, _, err := s.ListPropertyPhotos(ctx, shop, property); err != nil || len(rows) != 0 {
		t.Fatal("failed upload persisted data")
	}
	server.photoProcessing.Lock()
	w = upload(shop, "owner", "Busy caption", photoFixture(t), false)
	server.photoProcessing.Unlock()
	if w.Code != 503 || w.Header().Get("Retry-After") != "5" {
		t.Fatal("processing admission failed")
	}
	w = upload(shop, "owner", "Keep <script>caption</script>", photoFixture(t), false)
	if w.Code != 303 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	photos, _, _, err := s.ListPropertyPhotos(ctx, shop, property)
	if err != nil || len(photos) != 1 {
		t.Fatal("photo missing")
	}
	id := photos[0].ID
	for _, tc := range []struct {
		shop, role string
		status     int
	}{{other, "owner", 404}, {shop, "staff", 403}, {shop, "owner", 200}} {
		r := httptest.NewRequest("GET", "/property-photos/"+id, nil)
		r.SetPathValue("id", id)
		r = r.WithContext(contextWithUser(ctx, &User{ShopID: tc.shop, Role: tc.role}))
		w := httptest.NewRecorder()
		server.handlePropertyPhotoImage(w, r)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("image access: %d %+v", w.Code, w.Header())
		}
		if tc.status == 200 && (w.Header().Get("Content-Type") != "image/png" || w.Header().Get("X-Content-Type-Options") != "nosniff") {
			t.Fatal("image response headers missing")
		}
	}
	for _, confirmed := range []bool{false, true} {
		form := url.Values{}
		if confirmed {
			form.Set("confirm", "1")
		}
		r := httptest.NewRequest("POST", "/property-photos/"+id+"/delete", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", id)
		r = r.WithContext(contextWithUser(ctx, &User{ShopID: shop, Role: "owner"}))
		w := httptest.NewRecorder()
		server.handlePropertyPhotoDelete(w, r)
		want := 422
		if confirmed {
			want = 303
		}
		if w.Code != want {
			t.Fatalf("delete confirmation: %d", w.Code)
		}
	}
}

package app

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Server) handlePropertyPhotos(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	u := userFromContext(r.Context())
	if u == nil || u.Role != "owner" {
		http.Error(w, "Photos are available to the owner.", 403)
		return
	}
	p, err := s.store.GetPropertyAddress(r.Context(), u.ShopID, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	key, caption, category, message, status := "", "", "general", "", http.StatusOK
	if r.Method == http.MethodPost {
		if u.IsDemo {
			http.Error(w, "Photo uploads are not available in the public demo.", 403)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			writeRequestParseError(w, err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		key = r.PostForm.Get("photo_key")
		caption = strings.TrimSpace(r.PostForm.Get("caption"))
		category = r.PostForm.Get("category")
		if !validPhotoFields(key, caption, category) {
			message, status = errPhotoInput.Error(), 422
		} else {
			var photo normalizedPhoto
			photo, err = s.readPhotoUpload(r)
			if err == nil {
				_, err = s.store.AddPropertyPhoto(r.Context(), u.ShopID, p.ID, key, caption, category, u.Email, photo)
			}
			if err == nil {
				http.Redirect(w, r, "/properties/"+url.PathEscape(p.ID)+"/photos?saved=1", 303)
				return
			}
			switch {
			case errors.Is(err, errPhotoConflict):
				message, status = err.Error(), 409
			case errors.Is(err, errPhotoQuota):
				message, status = err.Error(), 422
			case errors.Is(err, errPhotoBusy):
				message, status = err.Error(), 503
				w.Header().Set("Retry-After", "5")
			case errors.Is(err, errPhotoImage), errors.Is(err, errPhotoOutput):
				message, status = err.Error(), 422
			default:
				log.Printf("save property photo: %v", err)
				message, status = "Could not confirm the upload. Retry with the same file and caption.", 503
			}
		}
	}
	if key == "" || !noteSubmissionKey.MatchString(key) {
		key, err = randomToken(16)
		if err != nil {
			http.Error(w, "Could not prepare upload. Reload and retry.", 503)
			return
		}
	}
	photos, count, used, err := s.store.ListPropertyPhotos(r.Context(), u.ShopID, p.ID)
	if err != nil {
		http.Error(w, "Could not load photos. Reload and retry.", 503)
		return
	}
	if status != 200 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.renderLayout(w, r, "Property photos — Sideyard", "property_photos_content", map[string]any{"Nav": NavData{Active: "customers", User: u}, "Property": p, "Photos": photos, "PhotoKey": key, "Caption": caption, "Category": category, "Categories": photoCategories, "Error": message, "Saved": r.Method == http.MethodGet && r.URL.Query().Get("saved") == "1", "Deleted": r.URL.Query().Get("deleted") == "1", "Count": count, "UsedMB": fmt.Sprintf("%.1f", float64(used)/(1<<20))})
}

var errPhotoBusy = errors.New("Another photo is being processed. Wait a few seconds, then select your file again and retry.")

func (s *Server) readPhotoUpload(r *http.Request) (normalizedPhoto, error) {
	if r.MultipartForm == nil || len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["photo"]) != 1 {
		return normalizedPhoto{}, errPhotoImage
	}
	if !s.photoProcessing.TryLock() {
		return normalizedPhoto{}, errPhotoBusy
	}
	defer s.photoProcessing.Unlock()
	file, header, err := r.FormFile("photo")
	if err != nil {
		return normalizedPhoto{}, errPhotoImage
	}
	defer file.Close()
	if header.Size > photoUploadLimit {
		return normalizedPhoto{}, errPhotoImage
	}
	data, err := io.ReadAll(io.LimitReader(file, photoUploadLimit+1))
	if err != nil {
		return normalizedPhoto{}, errPhotoImage
	}
	return normalizePhoto(data)
}

func (s *Server) handlePropertyPhotoImage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	u := userFromContext(r.Context())
	if u == nil || u.Role != "owner" {
		http.Error(w, "Photos are available to the owner.", 403)
		return
	}
	photo, err := s.store.GetPropertyPhoto(r.Context(), u.ShopID, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "Photo unavailable. Please retry.", 503)
		}
		return
	}
	w.Header().Set("Content-Type", photo.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(photo.Data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	_, _ = w.Write(photo.Data)
}

func (s *Server) handlePropertyPhotoDelete(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	if u == nil || u.Role != "owner" || u.IsDemo {
		http.Error(w, "Photo deletion is available to the owner outside the public demo.", 403)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form.", 400)
		return
	}
	if r.PostForm.Get("confirm") != "1" {
		http.Error(w, "Check the delete confirmation before deleting this photo.", 422)
		return
	}
	id, err := s.store.DeletePropertyPhoto(r.Context(), u.ShopID, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			log.Printf("delete property photo: %v", err)
			http.Error(w, "Could not confirm deletion. Please retry.", 503)
		}
		return
	}
	http.Redirect(w, r, "/properties/"+url.PathEscape(id)+"/photos?deleted=1", 303)
}

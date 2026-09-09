package photostore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type fixtureCredential struct{}

func (fixtureCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fixture-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type storedFixture struct {
	data []byte
	mime string
}

type fixtureTransport struct {
	mu         sync.Mutex
	objects    map[string]map[string]storedFixture
	latest     map[string]string
	writes     int
	versioning bool
}

func (f *fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// SDK generated requests can have lowercase map keys. Normalize them as an
	// HTTP server would when parsing the request from the wire.
	headers := make(http.Header)
	for name, values := range r.Header {
		for _, value := range values {
			headers.Add(name, value)
		}
	}
	r.Header = headers
	key := strings.TrimPrefix(r.URL.Path, "/photos/")
	h := make(http.Header)
	h.Set("x-ms-request-id", "fixture-request")
	response := &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader("")), Request: r}
	if r.Method == http.MethodPut {
		if r.Header.Get("If-None-Match") != "*" {
			return nil, fmt.Errorf("write lacked create-only condition")
		}
		if f.latest[key] != "" {
			response.StatusCode = 412
			h.Set("x-ms-error-code", "ConditionNotMet")
			response.Body = io.NopCloser(strings.NewReader("<Error><Code>ConditionNotMet</Code></Error>"))
			return response, nil
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		f.writes++
		version := fmt.Sprintf("version-%d", f.writes)
		mime := r.Header.Get("x-ms-blob-content-type")
		if mime == "" {
			mime = r.Header.Get("Content-Type")
		}
		f.objects[key] = map[string]storedFixture{version: {data: data, mime: mime}}
		f.latest[key] = version
		response.StatusCode = 201
		if f.versioning {
			h.Set("x-ms-version-id", version)
		}
		return response, nil
	}
	version := r.URL.Query().Get("versionid")
	if version == "" {
		version = f.latest[key]
	}
	object, exists := f.objects[key][version]
	if !exists {
		response.StatusCode = 404
		h.Set("x-ms-error-code", "BlobNotFound")
		return response, nil
	}
	h.Set("Content-Type", object.mime)
	h.Set("Content-Length", fmt.Sprint(len(object.data)))
	if f.versioning {
		h.Set("x-ms-version-id", version)
	}
	if r.Method == http.MethodGet {
		response.Body = io.NopCloser(bytes.NewReader(object.data))
	}
	return response, nil
}

func newFixture(t *testing.T) (*Azure, *fixtureTransport) {
	t.Helper()
	transport := &fixtureTransport{objects: map[string]map[string]storedFixture{}, latest: map[string]string{}, versioning: true}
	client, err := NewAzure("https://sideyardfixture.blob.core.windows.net", "photos", fixtureCredential{}, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	return client, transport
}

func TestCreateOnlyRetryAndExactVersionRead(t *testing.T) {
	client, transport := newFixture(t)
	key, content := strings.Repeat("a", 64), []byte("normalized fixture")
	ref, err := client.Put(context.Background(), key, "image/png", content)
	if err != nil {
		t.Fatal(err)
	}
	again, err := client.Put(context.Background(), key, "image/png", content)
	if err != nil || again != ref || transport.writes != 1 {
		t.Fatalf("retry did not preserve one immutable version: %+v %v writes=%d", again, err, transport.writes)
	}
	if _, err := client.Put(context.Background(), key, "image/png", []byte("different bytes")); err == nil {
		t.Fatal("different upload reused existing object")
	}
	transport.mu.Lock()
	transport.objects[key]["newer-version"] = storedFixture{data: []byte("newer contents"), mime: "image/png"}
	transport.latest[key] = "newer-version"
	transport.mu.Unlock()
	got, err := client.Get(context.Background(), ref)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("read followed latest instead of recorded version: %q %v", got, err)
	}
}

func TestUnversionedStorageAndDamagedObjectsFailClosed(t *testing.T) {
	client, transport := newFixture(t)
	transport.versioning = false
	if _, err := client.Put(context.Background(), strings.Repeat("b", 64), "image/png", []byte("fixture")); err == nil {
		t.Fatal("accepted an upload without versioning")
	}
	transport.versioning = true
	ref, err := client.Put(context.Background(), strings.Repeat("c", 64), "image/jpeg", []byte("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	transport.objects[ref.Key][ref.VersionID] = storedFixture{data: []byte("changed"), mime: "image/jpeg"}
	if _, err := client.Get(context.Background(), ref); err == nil {
		t.Fatal("accepted changed object bytes")
	}
	transport.objects[ref.Key][ref.VersionID] = storedFixture{data: []byte("fixture"), mime: "text/html"}
	if _, err := client.Get(context.Background(), ref); err == nil {
		t.Fatal("accepted changed content type")
	}
}

func TestEndpointAndReferenceBoundaries(t *testing.T) {
	for _, endpoint := range []string{"http://sideyardfixture.blob.core.windows.net", "https://example.com", "https://sideyardfixture.blob.core.windows.net/?sig=credential", "https://user@sideyardfixture.blob.core.windows.net"} {
		if err := ValidateEndpoint(endpoint); err == nil {
			t.Errorf("accepted %q", endpoint)
		}
	}
	client, transport := newFixture(t)
	if _, err := client.Put(context.Background(), "../other-tenant", "image/png", []byte("fixture")); err == nil || transport.writes != 0 {
		t.Fatal("invalid object key reached storage")
	}
	if _, err := client.Get(context.Background(), Reference{Key: strings.Repeat("a", 64), Size: 1}); err == nil {
		t.Fatal("accepted reference without an exact version and hash")
	}
}

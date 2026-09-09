// Package photostore stores normalized private photos as immutable Azure versions.
package photostore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
)

const MaxBytes = 4 << 20

var accountHost = regexp.MustCompile(`^[a-z0-9]{3,24}\.blob\.core\.windows\.net$`)
var objectKey = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Reference struct {
	Key, VersionID, SHA256, ContentType string
	Size                                int
}

type Azure struct {
	client    *azblob.Client
	container string
}

func ValidateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || !accountHost.MatchString(u.Host) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("photo storage requires an HTTPS Azure Blob account URL without credentials or query parameters")
	}
	return nil
}

func NewAzure(endpoint, container string, credential azcore.TokenCredential, transport *http.Client) (*Azure, error) {
	if err := ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	if container != "photos" || credential == nil || transport == nil {
		return nil, errors.New("private photos container, credential and HTTP client are required")
	}
	client, err := azblob.NewClient(endpoint, credential, &azblob.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: transport},
	})
	if err != nil {
		return nil, errors.New("initialize Azure photo storage")
	}
	return &Azure{client: client, container: container}, nil
}

// Put creates at most one object. A retry verifies the existing bytes and returns
// its exact version instead of overwriting it and creating billable duplicates.
func (a *Azure) Put(ctx context.Context, key, contentType string, data []byte) (Reference, error) {
	if !objectKey.MatchString(key) || len(data) < 1 || len(data) > MaxBytes || (contentType != "image/jpeg" && contentType != "image/png") {
		return Reference{}, errors.New("invalid normalized photo")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ref := Reference{Key: key, ContentType: contentType, Size: len(data), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	client := a.client.ServiceClient().NewContainerClient(a.container).NewBlockBlobClient(key)
	result, err := client.Upload(ctx, streaming.NopCloser(bytes.NewReader(data)), &blockblob.UploadOptions{
		HTTPHeaders: &blob.HTTPHeaders{BlobContentType: &contentType},
		Metadata:    map[string]*string{"sha256": &ref.SHA256},
		AccessConditions: &blob.AccessConditions{
			ModifiedAccessConditions: &blob.ModifiedAccessConditions{IfNoneMatch: to.Ptr(azcore.ETagAny)},
		},
	})
	if err == nil {
		if result.VersionID == nil || *result.VersionID == "" {
			return Reference{}, errors.New("photo storage versioning is not enabled")
		}
		ref.VersionID = *result.VersionID
		return ref, nil
	}
	var response *azcore.ResponseError
	if !errors.As(err, &response) || (response.StatusCode != http.StatusConflict && response.StatusCode != http.StatusPreconditionFailed) {
		return Reference{}, errors.New("photo storage upload failed")
	}
	properties, err := client.GetProperties(ctx, nil)
	if err != nil || properties.VersionID == nil || *properties.VersionID == "" {
		return Reference{}, errors.New("could not verify the existing photo version")
	}
	ref.VersionID = *properties.VersionID
	if _, err := a.Get(ctx, ref); err != nil {
		return Reference{}, err
	}
	return ref, nil
}

// Get only reads a recorded version. Callers authorize ownership in SQL first.
func (a *Azure) Get(ctx context.Context, ref Reference) ([]byte, error) {
	if !objectKey.MatchString(ref.Key) || ref.VersionID == "" || len(ref.SHA256) != 64 || ref.Size < 1 || ref.Size > MaxBytes {
		return nil, errors.New("invalid photo reference")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := a.client.ServiceClient().NewContainerClient(a.container).NewBlobClient(ref.Key).WithVersionID(ref.VersionID)
	if err != nil {
		return nil, errors.New("invalid photo version")
	}
	response, err := client.DownloadStream(ctx, nil)
	if err != nil {
		return nil, errors.New("photo storage download failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(ref.Size)+1))
	if err != nil {
		return nil, errors.New("photo version could not be read")
	}
	if len(data) != ref.Size || fmt.Sprintf("%x", sha256.Sum256(data)) != ref.SHA256 {
		return nil, errors.New("photo version bytes failed integrity verification")
	}
	if response.ContentType == nil || *response.ContentType != ref.ContentType {
		return nil, errors.New("photo version content type failed verification")
	}
	return data, nil
}

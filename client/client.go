package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/lsst-dm/s3nd/upload"
	"github.com/lsst-dm/s3nd/version"

	gherrors "github.com/pkg/errors"
)

type Client struct {
	server url.URL
}

func NewClient(server url.URL) *Client {
	return &Client{
		server: server,
	}
}

func (c *Client) Server() url.URL {
	return c.server
}

type UploadStatus struct {
	Server        url.URL
	RequestStatus *upload.RequestStatus
	Error         error
}

// Submit a upload request to the s3nd service.
func (c *Client) Upload(ctx context.Context, file string, uri url.URL) (*upload.RequestStatus, error) {
	endpoint := c.server
	endpoint.Path = "/upload"

	// http.PostForm is not used here because it does not accept a context
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint.String(), strings.NewReader(url.Values{
		"file": {file},
		"uri":  {uri.String()},
	}.Encode()))
	if err != nil {
		return nil, gherrors.Wrapf(err, "error sending file %q to uri %q", file, uri.String())
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, gherrors.Wrapf(err, "error sending file %q to uri %q", file, uri.String())
	}
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, gherrors.Wrapf(err, "error sending file %q to uri %q", file, uri.String())
	}

	status := upload.RequestStatus{}
	err = json.Unmarshal(body, &status)
	if err != nil {
		return nil, gherrors.Wrapf(err, "error sending file %q to uri %q", file, uri.String())
	}

	if r.StatusCode != 200 {
		return &status, gherrors.Errorf("error sending file %q to uri %q, msg %q", file, uri.String(), status.Msg)
	}

	return &status, nil
}

// Submit multiple upload requests to s3nd in parallel.
func (c *Client) UploadMulti(ctx context.Context, files map[string]url.URL) (*[]*UploadStatus, error) {
	var wg sync.WaitGroup
	statusCh := make(chan *UploadStatus, len(files))

	for f, uri := range files {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, err := c.Upload(ctx, f, uri)
			statusCh <- &UploadStatus{
				Server:        c.Server(),
				RequestStatus: status,
				Error:         err,
			}
		}()
	}

	wg.Wait()
	close(statusCh)

	var statuses []*UploadStatus
	var errs []error
	for s := range statusCh {
		statuses = append(statuses, s)
		if s.Error != nil {
			errs = append(errs, s.Error)
		}
	}

	if len(errs) > 0 {
		return &statuses, errors.Join(errs...)
	}

	return &statuses, nil
}

// Retrive s3nd version and configuration.
func (c *Client) Version(ctx context.Context) (*version.VersionInfo, error) {
	endpoint := c.server
	endpoint.Path = "/version"

	// http.Get is not used here because it does not accept a context
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
	if err != nil {
		return nil, gherrors.Wrapf(err, "error getting version info")
	}

	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, gherrors.Wrapf(err, "error getting version info")
	}
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, gherrors.Wrapf(err, "error getting version info")
	}

	status := version.VersionInfo{}
	err = json.Unmarshal(body, &status)
	if err != nil {
		return nil, gherrors.Wrapf(err, "error getting version info")
	}

	if r.StatusCode != 200 {
		return &status, gherrors.Errorf("error getting version info, status code %q", r.StatusCode)
	}

	return &status, nil
}

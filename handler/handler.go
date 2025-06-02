package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lsst-dm/s3nd/conf"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithy "github.com/aws/smithy-go"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sys/unix"
)

var (
	logger                  = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	errUploadAttemptTimeout = errors.New("upload attempt timeout")
)

type S3ndHandler struct {
	conf            *conf.S3ndConf
	awsConfig       *aws.Config
	s3Client        *s3.Client
	uploader        *manager.Uploader
	parallelUploads *semaphore.Weighted
}

type UploadTask struct {
	Id           uuid.UUID   `json:"id" swaggertype:"string" format:"uuid"`
	Uri          *RequestURL `json:"uri,omitempty" swaggertype:"string" example:"s3://my-bucket/my-key"`
	Bucket       *string     `json:"-"`
	Key          *string     `json:"-"`
	File         *string     `json:"file,omitempty" swaggertype:"string" example:"/path/to/file.txt"`
	StartTime    time.Time   `json:"-"`
	EndTime      time.Time   `json:"-"`
	Duration     string      `json:"duration,omitempty" example:"21.916462ms"`
	Attempts     int         `json:"attempts,omitzero" example:"1"`
	SizeBytes    int64       `json:"size_bytes,omitzero" example:"1000"`
	UploadParts  int64       `json:"upload_parts,omitempty" example:"1"`
	TransferRate string      `json:"transfer_rate,omitempty" example:"1000B/s"`
} //@name task

func NewUploadTask(startTime time.Time) *UploadTask {
	return &UploadTask{
		Id:        uuid.New(),
		StartTime: startTime,
	}
}

// the task is stopped because of an error and no data was sent
func (t *UploadTask) StopNoUpload() {
	t.Stop()
	// no transfer rate if we didn't start the upload
	t.TransferRate = ""
}

func (t *UploadTask) Stop() {
	t.EndTime = time.Now()
	duration := t.EndTime.Sub(t.StartTime)
	t.Duration = duration.String()
	t.TransferRate = fmt.Sprintf("%.3fMbit/s", float64(t.SizeBytes*8)/duration.Seconds()/(1<<20))
}

type RequestURL struct{ url.URL }

func (u RequestURL) MarshalText() ([]byte, error) {
	return []byte(u.String()), nil
}

type RequestStatus struct {
	Code int         `json:"code" example:"200"`
	Msg  string      `json:"msg,omitempty" example:"upload succeeded"`
	Task *UploadTask `json:"task,omitempty"`
} //@name requestStatus200

// requestStatusSwag400 is used only for Swagger documentation
//
//nolint:unused
type requestStatusSwag400 struct {
	RequestStatus
	Code int    `json:"code" example:"400"`
	Msg  string `json:"msg,omitempty" example:"error parsing request: missing field: uri"`
	Task *struct {
		Id       uuid.UUID `json:"id" swaggertype:"string" format:"uuid"`
		File     *string   `json:"file,omitempty" swaggertype:"string" example:"/path/to/file.txt"`
		Duration string    `json:"duration,omitempty" example:"37.921µs"`
	} `json:"task,omitempty"`
} //@name requestStatus400

// requestStatusSwag500 is used only for Swagger documentation
//
//nolint:unused
type requestStatusSwag500 struct {
	RequestStatus
	Code int    `json:"code" example:"500"`
	Msg  string `json:"msg,omitempty" example:"upload attempt 5/5 timeout: operation error S3: PutObject, context deadline exceeded"`
	Task *struct {
		UploadTask
		Duration string `json:"duration,omitempty" example:"37.921µs"`
		Attempts int    `json:"attempts,omitzero" example:"5"`
	} `json:"task,omitempty"`
} //@name requestStatus500

// requestStatusSwag504 is used only for Swagger documentation
//
//nolint:unused
type requestStatusSwag504 struct {
	RequestStatus
	Code int    `json:"code" example:"504"`
	Msg  string `json:"msg,omitempty" example:"upload queue timeout: context deadline exceeded"`
	Task *struct {
		Id       uuid.UUID   `json:"id" swaggertype:"string" format:"uuid"`
		Uri      *RequestURL `json:"uri,omitempty" swaggertype:"string" example:"s3://my-bucket/my-key"`
		File     *string     `json:"file,omitempty" swaggertype:"string" example:"/path/to/file.txt"`
		Duration string      `json:"duration,omitempty" example:"56.115µs"`
	} `json:"task,omitempty"`
} //@name requestStatus504

func NewHandler(conf *conf.S3ndConf) *S3ndHandler {
	handler := &S3ndHandler{
		conf: conf,
	}

	maxConns := int(*conf.UploadMaxParallel * 5) // allow for multipart upload creation

	var httpClient *awshttp.BuildableClient

	defaultTransportOtptions := func(t *http.Transport) {
		t.ExpectContinueTimeout = 0
		t.IdleConnTimeout = 0
		t.MaxIdleConns = maxConns
		t.MaxConnsPerHost = maxConns
		t.MaxIdleConnsPerHost = maxConns
		t.WriteBufferSize = int(conf.UploadWriteBufferSize.Value())
		// disable http/2 to prevent muxing over a single tcp connection
		t.ForceAttemptHTTP2 = false
		t.TLSClientConfig.NextProtos = []string{"http/1.1"}
	}

	if conf.UploadBwlimit.Value() != 0 {
		dialer := &net.Dialer{
			Control: func(network, address string, conn syscall.RawConn) error {
				// https://pkg.go.dev/syscall#RawConn
				var operr error
				if err := conn.Control(func(fd uintptr) {
					operr = syscall.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MAX_PACING_RATE, int(conf.UploadBwlimit.Value()/8))
				}); err != nil {
					return err
				}
				return operr
			},
		}

		httpClient = awshttp.NewBuildableClient().WithTransportOptions(func(t *http.Transport) {
			defaultTransportOtptions(t)
			t.DialContext = dialer.DialContext
		})
	} else {
		httpClient = awshttp.NewBuildableClient().WithTransportOptions(defaultTransportOtptions)
	}

	awsCfg, err := config.LoadDefaultConfig(
		context.TODO(),
		config.WithBaseEndpoint(*conf.EndpointUrl),
		config.WithHTTPClient(httpClient),
	)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	handler.awsConfig = &awsCfg

	handler.s3Client = s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true
		o.Retryer = aws.NopRetryer{} // we handle retries ourselves
	})

	handler.uploader = manager.NewUploader(handler.s3Client, func(u *manager.Uploader) {
		u.Concurrency = 1000
		u.MaxUploadParts = 1000
		u.PartSize = conf.UploadPartsize.Value()
	})

	handler.parallelUploads = semaphore.NewWeighted(*conf.UploadMaxParallel)

	return handler
}

// @Summary      upload file to S3
// @Tags         uploads
// @Accept       x-www-form-urlencoded
// @Produce      json
// @Param        uri  formData  string true  "Destination S3 URI"
// @Param        file formData  string true  "path to file to upload"
// @Success      200  {object}  RequestStatus
// @Failure      400  {object}  requestStatusSwag400
// @Failure      500  {object}  requestStatusSwag500
// @Failure      504  {object}  requestStatusSwag504
// @Router       /upload [post]
// @Header       400,500,504 {string} X-Error "error message"
func (h *S3ndHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	status := h.doServeHTTP(r)
	if status.Code == http.StatusOK {
		logger.Info(
			status.Msg,
			slog.Int("code", status.Code),
			slog.Any("task", status.Task),
		)
	} else {
		logger.Error(
			status.Msg,
			slog.Int("code", status.Code),
			slog.Any("task", status.Task),
		)
		w.Header().Set("x-error", status.Msg)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status.Code)
	_ = json.NewEncoder(w).Encode(status)
}

func (h *S3ndHandler) doServeHTTP(r *http.Request) RequestStatus {
	// create starting timestamp as early as possible
	task := NewUploadTask(time.Now())

	err := h.parseRequest(task, r)
	if err != nil {
		task.StopNoUpload()
		return RequestStatus{
			Code: http.StatusBadRequest,
			Msg:  errors.Wrapf(err, "error parsing request").Error(),
			Task: task,
		}
	}

	logger.Info(
		"queueing",
		slog.Any("task", task),
	)

	// limit the number of parallel uploads
	semaCtx, cancel := context.WithTimeout(r.Context(), *h.conf.QueueTimeout)
	defer cancel()
	if err := h.parallelUploads.Acquire(semaCtx, task.UploadParts); err != nil {
		task.StopNoUpload()
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.Wrap(err, "upload queue timeout")
		} else {
			err = errors.Wrap(err, "unable to aquire upload queue semaphore")
		}
		return RequestStatus{
			Code: http.StatusGatewayTimeout,
			Msg:  err.Error(),
			Task: task,
		}
	}
	defer h.parallelUploads.Release(task.UploadParts)

	logger.Info(
		"upload starting",
		slog.Any("task", task),
	)

	if err := h.uploadFileMultipart(r.Context(), task); err != nil {
		task.Stop()
		return RequestStatus{
			Code: http.StatusInternalServerError,
			Msg:  err.Error(),
			Task: task,
		}
	}

	task.Stop()

	return RequestStatus{
		Code: http.StatusOK,
		Msg:  "upload succeeded",
		Task: task,
	}
}

func (h *S3ndHandler) parseRequest(task *UploadTask, r *http.Request) error {
	{
		file := r.PostFormValue("file")
		if file == "" {
			return fmt.Errorf("missing field: file")
		}

		if !filepath.IsAbs(file) {
			return fmt.Errorf("only absolute file paths are supported: %q", html.EscapeString(file))
		}

		task.File = &file
	}

	{
		uriRaw := r.PostFormValue("uri")
		if uriRaw == "" {
			return fmt.Errorf("missing field: uri")
		}

		uri, err := url.Parse(uriRaw)
		if err != nil {
			return fmt.Errorf("unable to parse URI: %q", html.EscapeString(uriRaw))
		}

		if uri.Scheme != "s3" {
			return fmt.Errorf("only s3 scheme is supported: %q", html.EscapeString(uriRaw))
		}

		bucket := uri.Host
		if bucket == "" {
			return fmt.Errorf("unable to parse bucket from URI: %q", html.EscapeString(uriRaw))
		}

		key := uri.Path[1:] // Remove leading slash

		task.Uri = &RequestURL{*uri}
		task.Bucket = &bucket
		task.Key = &key
	}

	// obtain file size to determine the number of upload parts and to compute
	// the transfer rate later
	fStat, err := os.Stat(*task.File)
	if err != nil {
		return errors.Wrapf(err, "could not stat file %v", *task.File)
	}
	task.SizeBytes = fStat.Size()
	task.UploadParts = divCeil(task.SizeBytes, h.conf.UploadPartsize.Value())

	return nil
}

func (h *S3ndHandler) uploadFileMultipart(ctx context.Context, task *UploadTask) error {
	file, err := os.Open(*task.File)
	if err != nil {
		return errors.Wrapf(err, "Could not open file %v to upload", *task.File)
	}
	defer file.Close()

	maxAttempts := *h.conf.UploadTries
	for task.Attempts = 1; task.Attempts <= maxAttempts; task.Attempts++ {
		uploadCtx, cancel := context.WithTimeoutCause(ctx, *h.conf.UploadTimeout, errUploadAttemptTimeout)
		defer cancel()
		_, err = h.uploader.Upload(uploadCtx, &s3.PutObjectInput{
			Bucket: aws.String(*task.Bucket),
			Key:    aws.String(*task.Key),
			Body:   file,
		})
		if err != nil {
			var apiErr smithy.APIError
			cause := context.Cause(uploadCtx)

			switch {
			case errors.As(err, &apiErr):
				if apiErr.ErrorCode() == "NoSuchBucket" {
					return errors.Wrapf(err, "upload failed because the bucket %v does not exist", *task.Bucket)
				}
			case errors.Is(cause, errUploadAttemptTimeout):
				errMsg := fmt.Sprintf("upload attempt %v/%v timeout", task.Attempts, maxAttempts)

				// bubble up the error if we've exhausted our attempts
				if task.Attempts == maxAttempts {
					return errors.Wrap(err, errMsg)
				}
				// otherwise, log the timeout and carry on
				logger.Warn(errMsg, slog.Any("task", task))
				continue
			case errors.Is(err, context.Canceled):
				// the parent context was cancelled
				return errors.Wrapf(err, "upload attempt %v cancelled, probably because the client disconnected", task.Attempts)
			}

			// unknown error -- could be a server side problem so could retry
			errMsg := fmt.Sprintf("unknown error during upload attempt %v/%v", task.Attempts, maxAttempts)
			if task.Attempts == maxAttempts {
				return errors.Wrap(err, errMsg)
			}
			logger.Warn(errMsg, slog.Any("task", task))
		} else {
			break
		}
	}

	return nil
}

func divCeil(a, b int64) int64 {
	if b == 0 {
		panic("division by zero")
	}
	return (a + b - 1) / b // rounds up
}

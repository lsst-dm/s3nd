package conf

import (
	"flag"
	"log/slog"
	"os"
	"strconv"
	"time"

	k8sresource "k8s.io/apimachinery/pkg/api/resource"
)

var logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))

type S3ndConf struct {
	Host                  *string
	Port                  *int
	EndpointUrl           *string
	UploadMaxParallel     *int64
	UploadTimeout         *time.Duration
	QueueTimeout          *time.Duration
	UploadTries           *int
	UploadPartsize        *k8sresource.Quantity
	UploadBwlimit         *k8sresource.Quantity
	UploadWriteBufferSize *k8sresource.Quantity
}

// Parse the environment variables and flags. If a flag is not set, the
// environment variable is used. Errors are fatal.
func NewConf() S3ndConf {
	conf := S3ndConf{}

	// start flags
	defaultHost, ok := os.LookupEnv("S3ND_HOST")
	if !ok {
		defaultHost = "localhost"
	}
	conf.Host = flag.String("host", defaultHost, "S3 Daemon Host (S3ND_HOST)")

	defaultPort, _ := strconv.Atoi(os.Getenv("S3ND_PORT"))
	if defaultPort == 0 {
		defaultPort = 15555
	}
	conf.Port = flag.Int("port", defaultPort, "S3 Daemon Port (S3ND_PORT)")

	conf.EndpointUrl = flag.String("endpoint-url", os.Getenv("S3ND_ENDPOINT_URL"), "S3 Endpoint URL (S3ND_ENDPOINT_URL)")

	var defaultUploadMaxParallel int64
	defaultUploadMaxParallel, _ = strconv.ParseInt(os.Getenv("S3ND_UPLOAD_MAX_PARALLEL"), 10, 64)
	if defaultUploadMaxParallel == 0 {
		defaultUploadMaxParallel = 100
	}
	conf.UploadMaxParallel = flag.Int64("upload-max-parallel", defaultUploadMaxParallel, "Maximum number of parallel object uploads (S3ND_UPLOAD_MAX_PARALLEL)")

	defaultUploadTimeout := os.Getenv("S3ND_UPLOAD_TIMEOUT")
	if defaultUploadTimeout == "" {
		defaultUploadTimeout = "10s"
	}
	uploadTimeout := flag.String("upload-timeout", defaultUploadTimeout, "Upload Timeout (S3ND_UPLOAD_TIMEOUT)")

	defaultQueueTimeout := os.Getenv("S3ND_QUEUE_TIMEOUT")
	if defaultQueueTimeout == "" {
		defaultQueueTimeout = "10s"
	}
	queueTimeout := flag.String("queue-timeout", defaultQueueTimeout, "Queue Timeout waiting for transfer to start (S3ND_QUEUE_TIMEOUT)")

	defaultUploadTries, _ := strconv.Atoi(os.Getenv("S3ND_UPLOAD_TRIES"))
	if defaultUploadTries == 0 {
		defaultUploadTries = 1
	}
	conf.UploadTries = flag.Int("upload-tries", defaultUploadTries, "Max number of upload tries (S3ND_UPLOAD_TRIES)")

	defaultUploadPartsize := os.Getenv("S3ND_UPLOAD_PARTSIZE")
	if defaultUploadPartsize == "" {
		defaultUploadPartsize = "5Mi"
	}
	uploadPartsizeRaw := flag.String("upload-partsize", defaultUploadPartsize, "Upload Part Size (S3ND_UPLOAD_PARTSIZE)")

	defaultUploadBwlimit := os.Getenv("S3ND_UPLOAD_BWLIMIT")
	if defaultUploadBwlimit == "" {
		defaultUploadBwlimit = "0"
	}
	uploadBwlimitRaw := flag.String("upload-bwlimit", defaultUploadBwlimit, "Upload aggregate bandwidth limit in bits per second (S3ND_UPLOAD_BWLIMIT)")

	defaultUploadWriteBufferSize := os.Getenv("S3ND_UPLOAD_WRITE_BUFFER_SIZE")
	if defaultUploadWriteBufferSize == "" {
		defaultUploadWriteBufferSize = "64Ki"
	}
	uploadWriteBufferSizeRaw := flag.String("upload-write-buffer-size", defaultUploadWriteBufferSize, "Upload Write Buffer Size (S3ND_UPLOAD_WRITE_BUFFER_SIZE)")

	flag.Parse()
	// end flags

	if *conf.EndpointUrl == "" {
		logger.Error("S3ND_ENDPOINT_URL is required")
		os.Exit(1)
	}

	uploadTimeoutDuration, err := time.ParseDuration(*uploadTimeout)
	if err != nil {
		logger.Error("S3ND_UPLOAD_TIMEOUT is invalid")
		os.Exit(1)
	}
	conf.UploadTimeout = &uploadTimeoutDuration

	queueTimeoutDuration, err := time.ParseDuration(*queueTimeout)
	if err != nil {
		logger.Error("S3ND_QUEUE_TIMEOUT is invalid")
		os.Exit(1)
	}
	conf.QueueTimeout = &queueTimeoutDuration

	uploadPartsize, err := k8sresource.ParseQuantity(*uploadPartsizeRaw)
	if err != nil {
		logger.Error("S3ND_UPLOAD_PARTSIZE is invalid")
		os.Exit(1)
	}
	conf.UploadPartsize = &uploadPartsize

	uploadBwlimit, err := k8sresource.ParseQuantity(*uploadBwlimitRaw)
	if err != nil {
		logger.Error("S3ND_UPLOAD_BWLIMIT is invalid")
		os.Exit(1)
	}
	conf.UploadBwlimit = &uploadBwlimit

	uploadWriteBufferSize, err := k8sresource.ParseQuantity(*uploadWriteBufferSizeRaw)
	if err != nil {
		logger.Error("S3ND_UPLOAD_WRITE_BUFFER_SIZE is invalid")
		os.Exit(1)
	}
	conf.UploadWriteBufferSize = &uploadWriteBufferSize

	// report the configuration using the name of env vars instead of the internal field names.
	envVars := map[string]string{
		"S3ND_HOST":                     *conf.Host,
		"S3ND_PORT":                     strconv.Itoa(*conf.Port),
		"S3ND_ENDPOINT_URL":             *conf.EndpointUrl,
		"S3ND_UPLOAD_MAX_PARALLEL":      strconv.FormatInt(*conf.UploadMaxParallel, 10),
		"S3ND_UPLOAD_TIMEOUT":           (*conf.UploadTimeout).String(),
		"S3ND_QUEUE_TIMEOUT":            (*conf.QueueTimeout).String(),
		"S3ND_UPLOAD_TRIES":             strconv.Itoa(*conf.UploadTries),
		"S3ND_UPLOAD_PARTSIZE":          conf.UploadPartsize.String(),
		"S3ND_UPLOAD_BWLIMIT":           conf.UploadBwlimit.String(),
		"S3ND_UPLOAD_WRITE_BUFFER_SIZE": conf.UploadWriteBufferSize.String(),
	}
	logger.Info("service configuration", "conf", envVars)

	return conf
}

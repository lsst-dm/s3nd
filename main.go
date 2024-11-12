package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type s3dConf struct {
	host         *string
	port         *int
	endpoint_url *string
	// access_key   *string
	// secret_key   *string
}

type S3DHandler struct {
	Ctx       context.Context
	AwsConfig *aws.Config
	S3Client  *s3.Client
	Conf      *s3dConf
}

// UploadFile reads from a file and puts the data into an object in a bucket.
func (h *S3DHandler) UploadFile(ctx context.Context, bucketName string, objectKey string, fileName string) error {
	start := time.Now()
	file, err := os.Open(fileName)
	if err != nil {
		log.Printf("Couldn't open file %v to upload. Here's why: %v\n", fileName, err)
	} else {
		defer file.Close()
		_, err = h.S3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(objectKey),
			Body:   file,
		})
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "EntityTooLarge" {
				log.Printf("Error while uploading object to %s. The object is too large.\n"+
					"To upload objects larger than 5GB, use the S3 console (160GB max)\n"+
					"or the multipart upload API (5TB max).", bucketName)
			} else {
				log.Printf("Couldn't upload file %v to %v:%v. Here's why: %v\n",
					fileName, bucketName, objectKey, err)
			}
		} else {
			/*
				err = s3.NewObjectExistsWaiter(h.S3Client).Wait(
					ctx, &s3.HeadObjectInput{Bucket: aws.String(bucketName), Key: aws.String(objectKey)}, time.Minute)
				if err != nil {
					log.Printf("Failed attempt to wait for object %s to exist.\n", objectKey)
				}
			*/
		}
	}
	fmt.Printf("uploaded %v to %v:%v in %s\n", fileName, bucketName, objectKey, time.Now().Sub(start))
	return err
}

// UploadObject uses the S3 upload manager to upload an object to a bucket.
func (h *S3DHandler) UploadFileMultipart(bucket string, key string, fileName string) error {
	start := time.Now()
	// file, err := os.Open(fileName)
	data, err := ioutil.ReadFile(fileName)
	if err != nil {
		log.Printf("Couldn't open file %v to upload. Here's why: %v\n", fileName, err)
		return err
	}
	// defer file.Close()
	fmt.Printf("slurped %v:%v in %s\n", bucket, key, time.Now().Sub(start))

	s3Client := s3.NewFromConfig(*h.getAwsConfig(), func(o *s3.Options) {
		o.UsePathStyle = true
	})
	fmt.Printf("NewFromConfig %v:%v in %s\n", bucket, key, time.Now().Sub(start))
	uploader := manager.NewUploader(s3Client, func(u *manager.Uploader) {
		u.Concurrency = 1000
		u.MaxUploadParts = 1000
		u.PartSize = 1024 * 1024 * 5
	})
	fmt.Printf("NewUploader %v:%v in %s\n", bucket, key, time.Now().Sub(start))

	_, err = uploader.Upload(h.Ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader([]byte(data)),
		// Body:   file,
		// ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
	})
	if err != nil {
		var noBucket *types.NoSuchBucket
		if errors.As(err, &noBucket) {
			log.Printf("Bucket %s does not exist.\n", bucket)
			err = noBucket
		}
	}
	fmt.Printf("uploaded %v:%v in %s\n", bucket, key, time.Now().Sub(start))
	return err
}

func (h *S3DHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	file := r.PostFormValue("file")
	if file == "" {
		w.Header().Set("x-missing-field", "file")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	uri := r.PostFormValue("uri")
	if uri == "" {
		w.Header().Set("x-missing-field", "uri")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	fmt.Println("file:", file)
	fmt.Println("uri:", uri)

	if !filepath.IsAbs(file) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Only absolute file paths are supported, %q", html.EscapeString(file))
		return
	}

	u, err := url.Parse(uri)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Unable to parse URI, %q", html.EscapeString(uri))
		return
	}

	if u.Scheme != "s3" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Only s3 scheme is supported, %q", html.EscapeString(uri))
		return
	}

	bucket := u.Host
	if bucket == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Unable to parse bucket from URI, %q", html.EscapeString(uri))
		return
	}
	key := u.Path[1:] // Remove leading slash

	// fmt.Println("Bucket:", bucket)
	// fmt.Println("Key:", key)

	// err = h.UploadFile(context.Background(), bucket, key, file)
	// if err != nil {
	// 	w.WriteHeader(http.StatusBadRequest)
	// 	fmt.Printf("error uploading file: %s\n", err)
	// 	return
	// }
	err = h.UploadFileMultipart(bucket, key, file)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Printf("error uploading file: %s\n", err)
		return
	}

	fmt.Fprintf(w, "Successful put %q", html.EscapeString(uri))
}

func getConf() s3dConf {
	conf := s3dConf{}
	conf.host = flag.String("host", os.Getenv("S3DAEMON_HOST"), "S3 Daemon Host")
	defaultPort, _ := strconv.Atoi(os.Getenv("S3DAEMON_PORT"))
	if defaultPort == 0 {
		defaultPort = 15555
	}
	conf.port = flag.Int("port", defaultPort, "S3 Daemon Port")
	conf.endpoint_url = flag.String("s3-endpoint-url", os.Getenv("S3_ENDPOINT_URL"), "S3 Endpoint URL")
	flag.Parse()

	if *conf.endpoint_url == "" {
		log.Fatal("s3-endpoint-url is required")
	}

	log.Println("host:", *conf.host)
	log.Println("port:", *conf.port)
	log.Println("s3-endpoint-url:", *conf.endpoint_url)

	return conf
}

func (h *S3DHandler) getAwsConfig() *aws.Config {
	httpClient := awshttp.NewBuildableClient().WithTransportOptions(func(t *http.Transport) {
		t.ExpectContinueTimeout = 0
		t.IdleConnTimeout = 0
		t.MaxIdleConns = 1000
		t.MaxConnsPerHost = 1000
		t.MaxIdleConnsPerHost = 1000
		// disable http/2 to prevent muxing over a single tcp connection
		t.TLSClientConfig.NextProtos = []string{"http/1.1"}
	})

	cfg, err := config.LoadDefaultConfig(
		context.TODO(),
		config.WithBaseEndpoint(*h.Conf.endpoint_url),
		config.WithHTTPClient(httpClient),
		// config.WithRetryer(func() aws.Retryer {
		// 	return retry.NewStandard(func(o *retry.StandardOptions) {
		// 		o.MaxAttempts = 10
		// 		o.MaxBackoff = time.Millisecond * 500
		// 		o.RateLimiter = ratelimit.None
		// 	})
		// }),
	)
	if err != nil {
		log.Fatal(err)
	}

	return &cfg
}

func main() {
	conf := getConf()

	// httpClient := &http.Client{
	// 	Transport: &http.Transport{
	// 		ExpectContinueTimeout: 0,
	// 		IdleConnTimeout:       0,
	// 		MaxConnsPerHost:       1000,
	// 		MaxIdleConns:          1000,
	// 		MaxIdleConnsPerHost:   1000,
	// 	},
	// }

	// 	httpClient := awshttp.NewBuildableClient().WithTransportOptions(func(t *http.Transport) {
	// 		t.ExpectContinueTimeout = 0
	// 		t.IdleConnTimeout = 0
	// 		t.MaxIdleConns = 1000
	// 		t.MaxConnsPerHost = 1000
	// 		t.MaxIdleConnsPerHost = 1000
	// 	})

	ctx := context.TODO()

	cfg, err := config.LoadDefaultConfig(
		ctx,
		config.WithBaseEndpoint(*conf.endpoint_url),
		//config.WithHTTPClient(httpClient),
		// config.WithRetryer(func() aws.Retryer {
		// 	return retry.NewStandard(func(o *retry.StandardOptions) {
		// 		o.MaxAttempts = 10
		// 		o.MaxBackoff = time.Millisecond * 500
		// 		o.RateLimiter = ratelimit.None
		// 	})
		// }),
	)
	if err != nil {
		log.Fatal(err)
	}

	//cfg.HTTPClient.(*http.Client).Transport.(*http.Transport).ExpectContinueTimeout = 0

	// v := reflect.ValueOf(cfg.HTTPClient)
	// fmt.Println("Type:", v.Type())
	// fmt.Println("Kind:", v.Kind())

	s3Client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})
	resp, err := s3Client.ListBuckets(context.TODO(), nil)
	if err != nil {
		log.Fatal(err)
	}

	handler := S3DHandler{
		Ctx:       ctx,
		AwsConfig: &cfg,
		S3Client:  s3Client,
		Conf:      &conf,
	}

	// Print out the list of buckets
	fmt.Println("Buckets:")
	for _, bucket := range resp.Buckets {
		fmt.Println(*bucket.Name)
	}

	http.Handle("/", &handler)

	addr := fmt.Sprintf("%s:%d", *conf.host, *conf.port)
	fmt.Println("Listening on", addr)

	err = http.ListenAndServe(addr, nil)
	if errors.Is(err, http.ErrServerClosed) {
		fmt.Printf("server closed\n")
	} else if err != nil {
		fmt.Printf("error starting server: %s\n", err)
		os.Exit(1)
	}
}

package s3nd_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/lsst-dm/s3nd/client"
	"github.com/lsst-dm/s3nd/upload"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	minio "github.com/minio/minio-go/v7"
)

var _ = Describe("POST /upload", func() {
	testFile := "test1"

	It("returns 200", func() {
		f, err := os.CreateTemp("", testFile)
		Expect(err).NotTo(HaveOccurred())
		defer os.Remove(f.Name())
		defer f.Close()
		for i := 0; i < 3; i++ {
			_, err = f.WriteString(testFile + "\n")
			Expect(err).NotTo(HaveOccurred())
		}
		_ = f.Sync()

		resp, err := http.PostForm(s3ndUrl.String()+"/upload",
			url.Values{"file": {f.Name()}, "uri": {"s3://" + s3ndBucket + "/" + testFile}})
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
	})

	It("created an s3 object", func() {
		o, err := s3.GetObject(context.Background(), s3ndBucket, testFile, minio.GetObjectOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer o.Close()

		data, err := io.ReadAll(o)
		Expect(err).NotTo(HaveOccurred())

		Expect(string(data)).To(MatchRegexp(fmt.Sprintf("(%s\n){3}", testFile)))
	})
})

var _ = Describe("client.Upload", func() {
	var status *upload.RequestStatus
	var uri url.URL
	var file string
	testFile := "test2"

	It("returns 200", func() {
		f, err := os.CreateTemp("", testFile)
		Expect(err).NotTo(HaveOccurred())
		defer os.Remove(f.Name())
		defer f.Close()
		for i := 0; i < 3; i++ {
			_, err = f.WriteString(testFile + "\n")
			Expect(err).NotTo(HaveOccurred())
		}
		_ = f.Sync()

		s3nd := client.NewClient(s3ndUrl)
		file = f.Name()
		uri = url.URL{
			Scheme: "s3",
			Host:   s3ndBucket,
			Path:   "/" + testFile,
		}

		status, err = s3nd.Upload(context.Background(), file, uri)
		Expect(err).NotTo(HaveOccurred())
		Expect(status.Code).To(Equal(http.StatusOK))
	})

	It("created an s3 object", func() {
		o, err := s3.GetObject(context.Background(), s3ndBucket, testFile, minio.GetObjectOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer o.Close()

		data, err := io.ReadAll(o)
		Expect(err).NotTo(HaveOccurred())

		Expect(string(data)).To(MatchRegexp(fmt.Sprintf("(%s\n){3}", testFile)))
	})

	It("has a populated RequestStatus", func() {
		Expect(status.Code).To(Equal(http.StatusOK))
		Expect(status.Msg).To(Equal("upload succeeded"))
		Expect(status.Task).ToNot(BeNil())

		task := status.Task
		Expect(task.Id).ToNot(BeEmpty())
		Expect(task.Uri).To(Equal(&upload.RequestURL{URL: uri}))
		Expect(task.Bucket).To(BeNil()) // unset
		Expect(task.Key).To(BeNil())    // unset
		Expect(task.File).To(Equal(&file))
		Expect(task.StartTime.IsZero()).To(BeTrue()) // unset
		Expect(task.EndTime.IsZero()).To(BeTrue())   // unset
		Expect(task.Duration).ToNot(BeEmpty())
		Expect(task.DurationSeconds).ToNot(BeZero())
		Expect(task.Attempts).To(Equal(1))
		Expect(task.SizeBytes).ToNot(BeZero())
		Expect(task.UploadParts).To(Equal(int64(1)))
		Expect(task.TransferRate).ToNot(BeEmpty())
		Expect(task.TransferRateMbits).ToNot(BeZero())
	})
})

var _ = Describe("client.UploadMulti", func() {
	var statuses *[]*client.UploadStatus
	testFiles := []string{
		"upload_multi1",
		"upload_multi2",
		"upload_multi3",
	}

	It("returns 200", func() {
		uploads := map[string]url.URL{}

		for _, testFile := range testFiles {
			f, err := os.CreateTemp("", testFile)
			Expect(err).NotTo(HaveOccurred())
			defer os.Remove(f.Name())
			defer f.Close()
			for i := 0; i < 3; i++ {
				_, err = f.WriteString(testFile + "\n")
				Expect(err).NotTo(HaveOccurred())
			}
			_ = f.Sync()
			uploads[f.Name()] = url.URL{
				Scheme: "s3",
				Host:   s3ndBucket,
				Path:   "/" + testFile,
			}
		}

		s3nd := client.NewClient(s3ndUrl)

		var err error
		statuses, err = s3nd.UploadMulti(context.TODO(), uploads)
		Expect(err).NotTo(HaveOccurred())
	})

	It("created s3 object(s))", func() {
		for _, testFile := range testFiles {
			o, err := s3.GetObject(context.Background(), s3ndBucket, testFile, minio.GetObjectOptions{})
			Expect(err).NotTo(HaveOccurred())
			defer o.Close()

			data, err := io.ReadAll(o)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(MatchRegexp(fmt.Sprintf("(%s\n){3}", testFile)))
		}
	})

	It("has populated UploadStatus(es)", func() {
		Expect(len(*statuses)).To(Equal(len(testFiles)))

		for _, uStatus := range *statuses {
			Expect(uStatus).ToNot(BeNil())

			Expect(uStatus.RequestStatus).ToNot(BeNil())
			Expect(uStatus.Error).To(BeNil())
			Expect(uStatus.RequestStatus.Code).To(Equal(http.StatusOK))
			Expect(uStatus.RequestStatus.Task).ToNot(BeNil())

			task := uStatus.RequestStatus.Task
			Expect(task.Id).ToNot(BeEmpty())
			Expect(task.Uri).ToNot(BeNil())
			Expect(task.Bucket).To(BeNil()) // unset
			Expect(task.Key).To(BeNil())    // unset
			Expect(task.File).ToNot(BeNil())
			Expect(task.StartTime).To(Equal(time.Time{})) // unset
			Expect(task.EndTime).To(Equal(time.Time{}))   // unset
			Expect(task.Duration).ToNot(BeEmpty())
			Expect(task.DurationSeconds).ToNot(BeZero())
			Expect(task.Attempts).To(Equal(1))
			Expect(task.SizeBytes).ToNot(BeZero())
			Expect(task.UploadParts).To(Equal(int64(1)))
			Expect(task.TransferRate).ToNot(BeEmpty())
			Expect(task.TransferRateMbits).ToNot(BeZero())
		}
	})
})

package s3nd_test

import (
	"context"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
)

var (
	minioC   *tcminio.MinioContainer // running MinIO container
	s3ndCmd  *exec.Cmd               // handle to the running s3nd binary
	s3ndHost = "localhost"           // host where s3nd listens for requests
	s3ndPort = "15566"               // port where s3nd listens for requests
	s3ndUrl  = url.URL{
		Scheme: "http",
		Host:   s3ndHost + ":" + s3ndPort,
	} // URL for s3nd
	s3ndBucket = "test"      // pre-created bucket used for testing
	s3         *minio.Client // MinIO client to interact with the test bucket
)

func TestS3nd(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "s3nd Integration Suite")
}

var _ = SynchronizedBeforeSuite(func() {
	ctx := context.Background()

	// start a minio instance
	minioC, err := tcminio.Run(
		ctx,
		"minio/minio:RELEASE.2025-05-24T17-08-30Z", // pin image for reproducibility
		tcminio.WithUsername("miniouser"),
		tcminio.WithPassword("miniopass"),
	)
	Expect(err).NotTo(HaveOccurred())

	endpoint, err := minioC.ConnectionString(ctx)
	Expect(err).NotTo(HaveOccurred())

	// create a test bucket as s3nd does not create buckets
	s3, err = minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(minioC.Username, minioC.Password, ""),
		Secure: false, // container is HTTP
	})
	Expect(err).NotTo(HaveOccurred())

	if err := s3.MakeBucket(ctx, s3ndBucket, minio.MakeBucketOptions{}); err != nil {
		exists, e2 := s3.BucketExists(ctx, s3ndBucket)
		Expect(e2).NotTo(HaveOccurred())
		Expect(exists).To(BeTrue())
	}

	// build s3nd
	bin, err := gexec.Build("github.com/lsst-dm/s3nd")
	Expect(err).NotTo(HaveOccurred())

	// start s3nd
	Expect(filepath.IsAbs(bin)).To(BeTrue()) // ensure the path is absolute
	s3ndCmd = exec.CommandContext(ctx, bin)  //nolint:gosec // bin has been validated
	s3ndCmd.Env = append(os.Environ(),
		"AWS_ACCESS_KEY_ID="+minioC.Username,
		"AWS_SECRET_ACCESS_KEY="+minioC.Password,
		"AWS_REGION=dne",
		"S3ND_ENDPOINT_URL=http://"+endpoint,
		"S3ND_HOST="+s3ndHost,
		"S3ND_PORT="+s3ndPort,
	)
	s3ndCmd.Stdout, s3ndCmd.Stderr = GinkgoWriter, GinkgoWriter
	Expect(s3ndCmd.Start()).To(Succeed())

	// wait until s3nd is listening
	dial := func() bool {
		c, err := net.DialTimeout("tcp", s3ndHost+":"+s3ndPort, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return true
		}
		return false
	}
	Eventually(dial, 5*time.Second).Should(BeTrue())
}, func() {})

var _ = SynchronizedAfterSuite(func() {
	// stop s3nd
	if s3ndCmd != nil && s3ndCmd.Process != nil {
		_ = s3ndCmd.Process.Signal(os.Interrupt) // graceful shutdown
		_, _ = s3ndCmd.Process.Wait()
	}
	gexec.CleanupBuildArtifacts()

	// stop minio container
	if minioC != nil {
		_ = testcontainers.TerminateContainer(minioC)
	}
}, func() {})

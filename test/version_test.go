package s3nd_test

import (
	"context"
	"net/http"

	"github.com/lsst-dm/s3nd/client"
	"github.com/lsst-dm/s3nd/version"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GET /version", func() {
	It("returns 200", func() {
		resp, err := http.Get(s3ndUrl.String() + "/version")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
	})
})

var _ = Describe("client.Version", func() {
	var info *version.VersionInfo

	It("returns 200", func() {
		s3nd := client.NewClient(s3ndUrl)

		var err error
		info, err = s3nd.Version(context.Background())
		Expect(err).NotTo(HaveOccurred())
	})

	It("has a populated VersionInfo", func() {
		Expect(info.Version).To(MatchRegexp(`^\d+\.\d+\.\d+(-\w+)?$`))
		Expect(info.Config).ToNot(BeEmpty())

		config := info.Config
		Expect(config).To(HaveKeyWithValue("S3ND_HOST", s3ndUrl.Hostname()))
		Expect(config).To(HaveKeyWithValue("S3ND_PORT", s3ndUrl.Port()))
		Expect(config).To(HaveKeyWithValue("S3ND_ENDPOINT_URL", MatchRegexp(`^http://localhost`)))
		Expect(config).To(HaveKeyWithValue("S3ND_UPLOAD_MAX_PARALLEL", "100"))
		Expect(config).To(HaveKeyWithValue("S3ND_UPLOAD_TIMEOUT", "10s"))
		Expect(config).To(HaveKeyWithValue("S3ND_QUEUE_TIMEOUT", "10s"))
		Expect(config).To(HaveKeyWithValue("S3ND_UPLOAD_TRIES", "1"))
		Expect(config).To(HaveKeyWithValue("S3ND_UPLOAD_PARTSIZE", "5Mi"))
		Expect(config).To(HaveKeyWithValue("S3ND_UPLOAD_BWLIMIT", "0"))
		Expect(config).To(HaveKeyWithValue("S3ND_UPLOAD_WRITE_BUFFER_SIZE", "64Ki"))
	})
})

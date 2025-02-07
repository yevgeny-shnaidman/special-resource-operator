package hash_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	"github.com/openshift-psap/special-resource-operator/pkg/hash"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const emptyHash = "12161962213042174405"

func TestHash(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Hash Suite")
}

var _ = Describe("TestFNV64a", func() {
	DescribeTable(
		"hash value",
		func(input, output string) {
			s, err := hash.FNV64a(input)

			Expect(err).NotTo(HaveOccurred())
			Expect(s).To(Equal(output))
		},
		Entry("empty string", "", "cbf29ce484222325"),
		Entry("random string", "special-resource-operator", "20db61ac8744a54a"),
	)
})

var _ = Describe("Annotate", func() {
	It("should work as expected", func() {
		obj := &unstructured.Unstructured{}

		err := hash.Annotate(obj)
		Expect(err).NotTo(HaveOccurred())
		Expect(obj.GetAnnotations()).To(HaveKeyWithValue("specialresource.openshift.io/hash", emptyHash))
	})
})

var _ = Describe("AnnotationEqual", func() {
	DescribeTable(
		"annotation",
		func(h string, m types.GomegaMatcher) {
			objOld := &unstructured.Unstructured{}
			objNew := &unstructured.Unstructured{}

			objNew.SetAnnotations(map[string]string{"specialresource.openshift.io/hash": h})

			isEqual, err := hash.AnnotationEqual(objNew, objOld)

			Expect(err).NotTo(HaveOccurred())
			Expect(isEqual).To(m)
		},
		Entry("bad annotation", "12345", BeFalse()),
		Entry("good annotation", emptyHash, BeTrue()),
	)
})

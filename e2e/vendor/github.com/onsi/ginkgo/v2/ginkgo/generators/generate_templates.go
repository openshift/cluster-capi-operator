package generators

<<<<<<< HEAD
var specText = `{{.BuildTags}}
package {{.Package}}
=======
var specText = `package {{.Package}}
>>>>>>> decf4d9d (Add vSphere infrastructureCluster template)

import (
	{{.GinkgoImport}}
	{{.GomegaImport}}

	{{if .ImportPackage}}"{{.PackageImportPath}}"{{end}}
)

var _ = {{.GinkgoPackage}}Describe("{{.Subject}}", func() {

})
`

<<<<<<< HEAD
var agoutiSpecText = `{{.BuildTags}}
package {{.Package}}
=======
var agoutiSpecText = `package {{.Package}}
>>>>>>> decf4d9d (Add vSphere infrastructureCluster template)

import (
	{{.GinkgoImport}}
	{{.GomegaImport}}
	"github.com/sclevine/agouti"
	. "github.com/sclevine/agouti/matchers"

	{{if .ImportPackage}}"{{.PackageImportPath}}"{{end}}
)

var _ = {{.GinkgoPackage}}Describe("{{.Subject}}", func() {
	var page *agouti.Page

	{{.GinkgoPackage}}BeforeEach(func() {
		var err error
		page, err = agoutiDriver.NewPage()
		{{.GomegaPackage}}Expect(err).NotTo({{.GomegaPackage}}HaveOccurred())
	})

	{{.GinkgoPackage}}AfterEach(func() {
		{{.GomegaPackage}}Expect(page.Destroy()).To({{.GomegaPackage}}Succeed())
	})
})
`

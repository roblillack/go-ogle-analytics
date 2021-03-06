package main

//protocol generator, a file is generated for each hittype

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"code.google.com/p/go.tools/imports"

	"github.com/PuerkitoBio/goquery"
	"github.com/huandu/xstrings"
)

const protocolV1 = "https://developers.google.com/analytics/devguides/collection/protocol/v1/parameters"

type HitType struct {
	Name,
	StructName string
	Fields     []*Field
	Indices    []string
	HitTypeIDs []string
}

type Field struct {
	Name,
	PrivateName,
	Docs,
	Param,
	ParamStr,
	Type,
	Default,
	MaxLen,
	Examples string
	Required bool
	HitTypes []string
	Indices  []string
}

//===================================

var allFields = []*Field{}

var hitTypes = map[string]*HitType{
	//special base type
	"client": &HitType{Name: "client"},
}

//====================================================
//the code template (split into separate strings for somewhat better readability)

func buildCode() string {

	var clientFields = `{{if eq .Name "client" }}	//Use TLS when Send()ing
	UseTLS bool
	HttpClient *http.Client
{{end}}`

	var fields = `{{range $index, $element := .Fields}}	{{.PrivateName}} {{.Type}}{{if not .Required}}
	{{.PrivateName}}Set bool{{end}}
{{end}}`

	var paramVal = `{{if eq .Type "string"}}h.{{.PrivateName}}{{end}}` +
		`{{if eq .Type "bool"}}bool2str(h.{{.PrivateName}}){{end}}` +
		`{{if eq .Type "int64"}}int2str(h.{{.PrivateName}}){{end}}` +
		`{{if eq .Type "float64"}}float2str(h.{{.PrivateName}}){{end}}`

	var params = `{{range $index, $element := .Fields}}{{if ne .Param ""}}{{if not .Required}}	if h.{{.PrivateName}}Set {
{{end}}		v.Add({{.ParamStr}}, ` + paramVal + `){{if not .Required}}
	}{{end}}
{{end}}{{end}}`

	var constructorDocs = `{{if ne .Name "client" }}// New{{ .StructName }} ` +
		`creates a new {{ .StructName }} Hit Type.` +
		`{{range $index, $element := .Fields}}
		{{if .Required}}{{.Docs}}{{end}}{{end}}`

	var constructorParams = `{{range $index, $element := .Fields}}{{if .Required}}` +
		`{{if ne $index 0}},{{end}}{{.PrivateName}} {{.Type}}` +
		`{{end}}{{end}}`

	var constructor = constructorDocs + `
func New{{ .StructName }}(` + constructorParams + `) *{{.StructName}}	{
	h := &{{ .StructName }}{
{{range $index, $element := .Fields}}{{if .Required}}		{{.PrivateName}}: {{.PrivateName}},
{{end}}{{end}}	}
	return h
}{{end}}
`

	var clientFuncs = `{{if eq .Name "client" }}
func (c *Client) setType(h hitType) {
	switch h.(type) {
{{range $index, $id := .HitTypeIDs}}	case *{{ exportName $id}}:
		c.hitType = "{{ $id }}"
{{end}}	}
}
{{end}}`

	var setterFuncs = `{{range $index, $element := .Fields}}{{if not .Required}}{{.Docs}}
func (h *{{$.StructName}}) {{.Name}}({{.PrivateName}} {{.Type}}) *{{$.StructName}}	{
	h.{{.PrivateName}} = {{.PrivateName}}
	h.{{.PrivateName}}Set = true
	return h
}

{{end}}{{end}}`

	return `package ga

//WARNING: This file was generated. Do not edit.

//{{.StructName}} Hit Type
type {{.StructName}} struct {
` + clientFields + fields + `}

` + constructor + clientFuncs + `

func (h *{{.StructName}}) addFields(v url.Values) error {
` + params + `	return nil
}

` + setterFuncs + `

func (h *{{.StructName}}) Copy() *{{.StructName}} {
	c := *h
	return &c
}
`
}

var codeTemplate *template.Template

//====================================================

//meta helpers
func grepper(restr string) func(string) string {
	re := regexp.MustCompile(restr)
	return func(s string) string {
		matches := re.FindAllStringSubmatch(s, 1)
		if len(matches) != 1 {
			log.Fatalf("'%s' should match '%s' exactly once", restr, s)
		}
		groups := matches[0]
		if len(groups) != 2 {
			log.Fatalf("'%s' should have exactly one group (found %d)", restr, len(groups))
		}
		return groups[1]
	}
}

func striper(restr string) func(string) string {
	re := regexp.MustCompile(restr)
	return func(s string) string {
		return re.ReplaceAllString(s, "")
	}
}

//helper functions
var trim = strings.TrimSpace
var preslash = grepper(`^([^\/]+)`)
var alpha = striper(`[^A-Za-z]`)
var indexMatcher = regexp.MustCompile(`<[A-Za-z]+>`)
var indexVar = grepper(`<([A-Za-z]+)>`)
var strVar = grepper(`'([a-z]+)'`)

func comment(s string) string {
	words := strings.Split(s, " ")
	comment := "//"
	width := 0
	for _, w := range words {
		if width > 55 {
			width = 0
			comment += "\n//"
		}
		width += len(w) + 1
		comment += " " + w
	}
	return comment
}

func exportName(s string) string {
	words := strings.Split(s, " ")
	for i, w := range words {
		words[i] = xstrings.FirstRuneToUpper(w)
	}
	return strings.Join(words, "")
}

func goName(parent, field string) string {
	return strings.TrimPrefix(field, parent)
}

func goType(gaType string) string {
	switch gaType {
	case "text":
		return "string"
	case "integer":
		return "int64"
	case "boolean":
		return "bool"
	case "currency":
		return "float64"
	}
	log.Fatal("Unknown GA Type: " + gaType)
	return ""
}

func displayErr(code string, err error) {
	lines := strings.Split(code, "\n")
	for i, l := range lines {
		lines[i] = fmt.Sprintf("%2d: %s", i+1, l)
	}
	log.Fatalf("Template:\n%s\n====\nError: %s", strings.Join(lines, "\n"), err)
}

//====================================

//main pipeline
func main() {
	check()
}

func check() {
	code := buildCode()
	t := template.New("ga-code")
	t = t.Funcs(template.FuncMap{
		"exportName": exportName,
	})
	t, err := t.Parse(code)
	if err != nil {
		displayErr(code, err)
	}
	codeTemplate = t
	parse()
}

func parse() {

	log.Println("reading: protocol.html")
	b, _ := ioutil.ReadFile("generate/protocol-v1.html")
	r := bytes.NewReader(b)
	doc, err := goquery.NewDocumentFromReader(r)

	// log.Println("loading: " + protocolV1)
	// doc, err := goquery.NewDocument(protocolV1)
	if err != nil {
		log.Fatal(err)
	}

	//special exluded field
	var hitTypeDocs string

	doc.Find("h3").Each(func(i int, s *goquery.Selection) {

		content := s.Next().Children()
		cells := content.Eq(2).Find("tr td")

		//get trimmed raw contents
		f := &Field{
			Name:     trim(s.Find("a").Text()),
			Required: !strings.Contains(content.Eq(0).Text(), "Optional"),
			Docs:     trim(content.Eq(1).Text()),
			Param:    trim(cells.Eq(0).Text()),
			Type:     trim(cells.Eq(1).Text()),
			Default:  trim(cells.Eq(2).Text()),
			MaxLen:   trim(cells.Eq(3).Text()),
			HitTypes: strings.Split(trim(cells.Eq(4).Text()), ", "),
			Examples: trim(content.Eq(3).Text()),
		}

		if f.Name == "Hit type" {
			hitTypeDocs = f.Docs
		}
		allFields = append(allFields, f)
	})

	buildTypes(hitTypeDocs)
}

func buildTypes(hitTypeDocs string) {
	log.Println("building types")
	//hit type docs contain all type names
	for _, t := range strings.Split(hitTypeDocs, ",") {
		t = strVar(t)
		hitTypes[t] = &HitType{Name: t}
	}

	//place each field in one or more types
	for _, f := range allFields {
		for _, t := range f.HitTypes {
			//the client type holds all the common fields
			if t == "all" {
				t = "client"
			}
			h, exists := hitTypes[t]
			if !exists {
				log.Fatalf("Unknown type: '%s'", t)
			}
			h.Fields = append(h.Fields, f)
		}
	}

	process()
}

func process() {
	log.Println("processing fields")
	for _, f := range allFields {
		processField(f)
	}
	for _, h := range hitTypes {
		processHitType(h)
	}
	generate()
}

func processField(f *Field) {

	f.Name = alpha(exportName(preslash(f.Name)))

	//trim the field name by the hittype name (prevents ga.Event.EventAction)
	for _, h := range f.HitTypes {
		f.Name = goName(exportName(h), f.Name)
	}

	f.PrivateName = xstrings.FirstRuneToLower(f.Name)

	//these are manually defaulted
	if f.Name == "ProtocolVersion" || f.Name == "ClientID" {
		f.Required = false
	}

	//special case: DOCS ARE WRONG
	//these are not optional
	if strings.Contains(f.Docs, "Must not be empty.") {
		f.Docs = strings.Replace(f.Docs, "Must not be empty.", "", 1)
		f.Required = true
	}

	//special case:

	//unexport the hit type field
	if f.Name == "HitType" {
		f.Name = xstrings.FirstRuneToLower(f.Name)
	}

	f.Docs = comment(trim(f.Docs))
	f.Type = goType(f.Type)
	f.ParamStr = `"` + f.Param + `"`

	//check param for <extraVars>
	for _, i := range indexMatcher.FindAllString(f.Param, -1) {
		//extract each var
		newi := indexVar(i)
		f.Indices = append(f.Indices, newi)
		//convert param var into a string concat
		f.ParamStr = strings.Replace(f.ParamStr, i, `" + h.`+newi+` + "`, 1)
	}
}

func processHitType(h *HitType) {

	h.StructName = exportName(h.Name)

	//set of index vars
	is := map[string]bool{}
	for _, f := range h.Fields {
		//add all index vars to the set
		for _, i := range f.Indices {
			is[i] = true
		}
	}
	//extra psuedo-fields to be added
	for i, _ := range is {
		h.Indices = append(h.Indices, i)
	}
	sort.Strings(h.Indices)

	//ordered indicies
	for _, i := range h.Indices {
		h.Fields = append(h.Fields, &Field{
			Name:        xstrings.FirstRuneToUpper(i),
			PrivateName: i,
			Type:        "string",
			Docs:        "// " + xstrings.FirstRuneToUpper(i) + " is required by other properties",
		})
	}

	//place all non-client hittype ids in client for templating
	if h.Name == "client" {
		for _, h2 := range hitTypes {
			if h2.Name != "client" {
				h.HitTypeIDs = append(h.HitTypeIDs, h2.Name)
			}
		}
		sort.Strings(h.HitTypeIDs)
	}
}

func generate() {
	log.Println("generating files")
	for _, h := range hitTypes {
		generateFile(h)
	}
}

func generateFile(h *HitType) {

	code := &bytes.Buffer{}
	codeTemplate.Execute(code, h)

	newCode, err := imports.Process("prog.go", code.Bytes(), &imports.Options{
		AllErrors: true,
		TabWidth:  4,
		Comments:  true,
	})
	if err != nil {
		displayErr(code.String(), err)
	}

	fname := "type-" + h.Name + ".go"
	f, err := os.Create(fname)
	defer f.Close()
	if err != nil {
		log.Fatal(err)
	}
	f.Write(newCode)
	log.Println("generated " + fname)
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package gowsdl

import (
	"bytes"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/iancoleman/strcase"
	"go/format"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"
	"unicode"
)

const maxRecursion uint8 = 20

var nsFileNameReplacements = map[string]string{
	"https://": "",
	"http://":  "",
	"-":        "_",
}

var nsPkgReplacements = map[string]string{
	"https://":        "",
	"http://":         "",
	"-":               "",
	"ws.polarion.com": "ws",
	"webservice":      "",
	"impl":            "",
	"types":           "",
}

// GoWSDL defines the struct for WSDL generator.
type GoWSDL struct {
	filePrefix            string
	dir                   string
	pkg                   string
	location              *Location
	rawWSDL               []byte
	ignoreTLS             bool
	makePublicFn          func(string) string
	wsdl                  *WSDL
	resolvedXSDExternals  map[string]bool
	currentRecursionLevel uint8
	typeResolver          *TypeResolver
	nsPkgReplacements     map[string]string
}

var cacheDir = filepath.Join(os.TempDir(), "gowsdl-cache")

func init() {
	err := os.MkdirAll(cacheDir, 0700)
	if err != nil {
		log.Println("Create cache directory", "error", err)
		os.Exit(1)
	}
}

var timeout = time.Duration(30 * time.Second)

func dialTimeout(network, addr string) (net.Conn, error) {
	return net.DialTimeout(network, addr, timeout)
}

func downloadFile(url string, ignoreTLS bool) ([]byte, error) {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: ignoreTLS,
		},
		Dial: dialTimeout,
	}
	client := &http.Client{Transport: tr}

	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Received response code %d", resp.StatusCode)
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// NewGoWSDL initializes WSDL generator.
func NewGoWSDL(wsdlFile, filePrefix string,
	dir string, pkg string, ignoreTLS bool, exportAllTypes bool, nsPkgReplacements map[string]string) (ret *GoWSDL, err error) {

	wsdlFile = strings.TrimSpace(wsdlFile)
	if wsdlFile == "" {
		return nil, errors.New("WSDL wsdlFile is required to generate Go proxy")
	}

	makePublicFn := func(id string) string { return id }
	if exportAllTypes {
		makePublicFn = makePublic
	}

	var location *Location
	if location, err = ParseLocation(wsdlFile); err != nil {
		return
	}

	ret = &GoWSDL{
		filePrefix:   filePrefix,
		dir:          dir,
		pkg:          pkg,
		location:     location,
		ignoreTLS:    ignoreTLS,
		makePublicFn: makePublicFn,
		typeResolver: NewTypeResolver(pkg),
	}
	return
}

// Generate initiaties the code generation process by starting two goroutines: one
// to generate Types and another one to generate Operations.
func (g *GoWSDL) Generate() (err error) {
	if err = g.unmarshal(); err != nil {
		return
	}

	g.typeResolver.RegisterTypes(g.wsdl)

	if err = g.genTypes(); err != nil {
		return
	}

	if err = g.genService(); err != nil {
		return
	}

	if err = g.genServer(); err != nil {
		return
	}
	return
}

func (g *GoWSDL) fetchFile(loc *Location) (data []byte, err error) {
	if loc.f != "" {
		log.Println("Reading", "file", loc.f)
		data, err = os.ReadFile(loc.f)
	} else {
		log.Println("Downloading", "file", loc.u.String())
		data, err = downloadFile(loc.u.String(), g.ignoreTLS)
	}
	return
}

func (g *GoWSDL) unmarshal() error {
	data, err := g.fetchFile(g.location)
	if err != nil {
		return err
	}

	g.wsdl = new(WSDL)
	err = xml.Unmarshal(data, g.wsdl)
	if err != nil {
		return err
	}
	g.rawWSDL = data

	for _, schema := range g.wsdl.Types.Schemas {
		err = g.resolveXSDExternals(schema, g.location)
		if err != nil {
			return err
		}
	}

	return nil
}

func (g *GoWSDL) resolveXSDExternals(schema *XSDSchema, loc *Location) error {
	download := func(base *Location, ref string) error {
		location, err := base.Parse(ref)
		if err != nil {
			return err
		}
		schemaKey := location.String()
		if g.resolvedXSDExternals[location.String()] {
			return nil
		}
		if g.resolvedXSDExternals == nil {
			g.resolvedXSDExternals = make(map[string]bool, maxRecursion)
		}
		g.resolvedXSDExternals[schemaKey] = true

		var data []byte
		if data, err = g.fetchFile(location); err != nil {
			return err
		}

		newschema := new(XSDSchema)

		err = xml.Unmarshal(data, newschema)
		if err != nil {
			return err
		}

		if (len(newschema.Includes) > 0 || len(newschema.Imports) > 0) &&
			maxRecursion > g.currentRecursionLevel {
			g.currentRecursionLevel++

			err = g.resolveXSDExternals(newschema, location)
			if err != nil {
				return err
			}
		}

		g.wsdl.Types.Schemas = append(g.wsdl.Types.Schemas, newschema)

		return nil
	}

	for _, impts := range schema.Imports {
		// Download the file only if we have a hint in the form of schemaLocation.
		if impts.SchemaLocation == "" {
			//log.Printf("[DEBUG] Don't know where to find XSD for %s", impts.Namespace)
			continue
		}

		if e := download(loc, impts.SchemaLocation); e != nil {
			return e
		}
	}

	for _, incl := range schema.Includes {
		if e := download(loc, incl.SchemaLocation); e != nil {
			return e
		}
	}

	return nil
}

type Context struct {
	resolver *NsTypeResolver
	wsdl     *GoWSDL
}

func NewContext(wsdl *GoWSDL) (ret *Context) {
	ret = &Context{wsdl: wsdl}
	ret.setNS(wsdl.wsdl.TargetNamespace)
	return
}

func (o *Context) Log(messages ...string) string {
	log.Printf("%v: %v\n", o.resolver.Schema.TargetNamespace, messages)
	return ""
}

func (o *Context) FindTypeNillable(xsdType string, nillable bool) (ret string) {
	return o.resolver.FindTypeNillable(xsdType, nillable)
}

func (o *Context) FindTypeNotNillable(xsdType string) (ret string) {
	return o.FindTypeNillable(xsdType, false)
}

func (o *Context) FindTypeName(message string) (ret string) {
	ret = o.FindTypeNotNillable(message)
	ret = o.removePackage(ret)
	return
}

func (o *Context) removePackage(ret string) string {
	if strings.Contains(ret, ".") {
		ret = strings.Split(ret, ".")[1]
	}
	return ret
}

func (o *Context) setNS(ns string) string {
	o.resolver = o.wsdl.typeResolver.GetResolverForNamespace(ns)
	if o.resolver == nil {
		log.Fatalf("namespace not registered: %v", ns)
	}
	return o.getNS()
}

// Method setNS returns the currently active XML namespace.
func (o *Context) getNS() string {
	return o.resolver.Schema.TargetNamespace
}

// Given a type, check if there's an Element with that type, and return its name.
func (o *Context) findNameByType(name string) (ret string) {
	ret = o.resolver.FindTypeNillable(name, true)
	//return newTraverser(nil, g.wsdl.Types.Schemas).findNameByType(name)
	return
}

func (o *Context) goPackage() (ret string) {
	return o.resolver.GetGoPackage()
}

func (o *Context) goImports() (ret string) {
	return o.resolver.GetGoImports()
}

func (g *GoWSDL) genTypes() (err error) {
	context := NewContext(g)
	funcMap := template.FuncMap{
		"log":                      context.Log,
		"findTypeNillable":         context.FindTypeNillable,
		"findType":                 context.FindTypeNotNillable,
		"findTypeName":             context.FindTypeName,
		"stripns":                  stripns,
		"replaceReservedWords":     replaceReservedWords,
		"replaceAttrReservedWords": replaceAttrReservedWords,
		"normalize":                normalize,
		"makePublic":               g.makePublicFn,
		"makeFieldPublic":          makePublic,
		"comment":                  comment,
		"removeNS":                 removeNS,
		"goString":                 goString,
		"removePointerFromType":    removePointerFromType,
		"getNS":                    context.getNS,
		"goPackage":                context.goPackage,
		"goImports":                context.goImports,
	}

	schemaToContent := map[string]*bytes.Buffer{}

	tmplHeader := template.Must(template.New("TypesHeader").Funcs(funcMap).Parse(schemaHeader))
	tmplBody := template.Must(template.New("TypesBody").Funcs(funcMap).Parse(schemaTmpl))

	for _, schema := range g.wsdl.Types.Schemas {
		context.setNS(schema.TargetNamespace)

		data := schemaToContent[schema.TargetNamespace]
		if data == nil {
			data = new(bytes.Buffer)
			schemaToContent[schema.TargetNamespace] = data
			if err = tmplHeader.Execute(data, schema); err != nil {
				return
			}
		}
		if err = tmplBody.Execute(data, schema); err != nil {
			return
		}
	}

	for namespace, data := range schemaToContent {
		if err = g.writeFile("types_", namespace, g.formatSource(data), ""); err != nil {
			return
		}
	}
	return
}

func (g *GoWSDL) writeFile(localFilePrefix string, targetNamespace string, source []byte, subDir string) (err error) {
	targetFolder := filepath.Join(g.dir, g.typeResolver.NamespaceToPackageRelative[targetNamespace], subDir)
	err = os.MkdirAll(targetFolder, 0744)

	var file *os.File
	targetFile := filepath.Join(targetFolder,
		g.filePrefix+localFilePrefix+g.typeResolver.NamespaceToFileName[targetNamespace]+".go")

	log.Printf("generate : %v, %v\n", targetNamespace, targetFile)
	if file, err = os.Create(targetFile); err != nil {
		return
	}
	defer file.Close()

	_, err = file.Write(source)
	return
}

func NamespaceToPackageRelative(namespace string) (ret string) {
	ret = strings.ToLower(namespace)
	for org, rep := range nsPkgReplacements {
		ret = strings.ReplaceAll(ret, org, rep)
	}
	ret = strings.TrimSpace(ret)
	ret = strings.TrimPrefix(ret, "/")
	ret = strings.TrimSuffix(ret, "/")
	return
}

func PackageLast(packageFull string) (ret string) {
	parts := strings.Split(packageFull, "/")
	ret = parts[len(parts)-1]
	if ret == "" {
		ret = parts[len(parts)-2]
	}
	return
}

func NamespaceToFileName(namespace string) (ret string) {
	ret = PackageLast(namespace)
	ret = strings.ToLower(ret)
	for org, rep := range nsFileNameReplacements {
		ret = strings.ReplaceAll(ret, org, rep)
	}
	ret = strcase.ToSnake(ret)
	return
}

func (g *GoWSDL) genService() (err error) {
	context := NewContext(g)
	funcMap := template.FuncMap{
		"findTypeNillable":     context.FindTypeNillable,
		"findType":             context.FindTypeNotNillable,
		"findTypeName":         context.FindTypeName,
		"stripns":              stripns,
		"replaceReservedWords": replaceReservedWords,
		"normalize":            normalize,
		"makePublic":           g.makePublicFn,
		"makePrivate":          makePrivate,
		"findSOAPAction":       g.findSOAPAction,
		"findServiceAddress":   g.findServiceAddress,
		"comment":              comment,
		"goPackage":            context.goPackage,
		"goImports":            context.goImports,
	}

	data := new(bytes.Buffer)
	tmpl := template.Must(template.New("Service").Funcs(funcMap).Parse(service))
	if err = tmpl.Execute(data, g.wsdl.PortTypes); err != nil {
		return
	}

	err = g.writeFile("service_", g.wsdl.TargetNamespace, g.formatSource(data), "")

	return
}

func (g *GoWSDL) genServer() (err error) {
	subDir := "mock"
	context := NewContext(g)
	funcMap := template.FuncMap{
		"findTypeNillable":     context.FindTypeNillable,
		"findType":             context.FindTypeNotNillable,
		"findTypeName":         context.FindTypeName,
		"stripns":              stripns,
		"replaceReservedWords": replaceReservedWords,
		"makePublic":           g.makePublicFn,
		"findSOAPAction":       g.findSOAPAction,
		"findServiceAddress":   g.findServiceAddress,
		"comment":              comment,
		"goPackage":            func() string { return subDir },
		"goImports":            context.goImports,
	}

	data := new(bytes.Buffer)

	var tmpl *template.Template
	tmpl = template.Must(template.New("ServerHeader").Funcs(funcMap).Parse(serverHeader))
	err = tmpl.Execute(data, "")
	data.Write([]byte("var wsdl = `" + string(g.rawWSDL) + "`"))
	tmpl = template.Must(template.New("Server").Funcs(funcMap).Parse(serverTmpl))
	err = tmpl.Execute(data, g.wsdl.PortTypes)

	err = g.writeFile("server_", g.wsdl.TargetNamespace, g.formatSource(data), subDir)
	return
}

func (g *GoWSDL) formatSource(data *bytes.Buffer) (ret []byte) {
	var err error
	if ret, err = format.Source(data.Bytes()); err != nil {
		log.Printf("format err: %v\n", err)
		ret = data.Bytes()
	}
	return
}

var reservedWords = map[string]string{
	"break":       "break_",
	"default":     "default_",
	"func":        "func_",
	"interface":   "interface_",
	"select":      "select_",
	"case":        "case_",
	"defer":       "defer_",
	"go":          "go_",
	"map":         "map_",
	"struct":      "struct_",
	"chan":        "chan_",
	"else":        "else_",
	"goto":        "goto_",
	"package":     "package_",
	"switch":      "switch_",
	"const":       "const_",
	"fallthrough": "fallthrough_",
	"if":          "if_",
	"range":       "range_",
	"type":        "type_",
	"continue":    "continue_",
	"for":         "for_",
	"import":      "import_",
	"return":      "return_",
	"var":         "var_",
}

var reservedWordsInAttr = map[string]string{
	"break":       "break_",
	"default":     "default_",
	"func":        "func_",
	"interface":   "interface_",
	"select":      "select_",
	"case":        "case_",
	"defer":       "defer_",
	"go":          "go_",
	"map":         "map_",
	"struct":      "struct_",
	"chan":        "chan_",
	"else":        "else_",
	"goto":        "goto_",
	"package":     "package_",
	"switch":      "switch_",
	"const":       "const_",
	"fallthrough": "fallthrough_",
	"if":          "if_",
	"range":       "range_",
	"type":        "type_",
	"continue":    "continue_",
	"for":         "for_",
	"import":      "import_",
	"return":      "return_",
	"var":         "var_",
	"string":      "astring",
}

// Replaces Go reserved keywords to avoid compilation issues
func replaceReservedWords(identifier string) string {
	value := reservedWords[identifier]
	if value != "" {
		return value
	}
	return normalize(identifier)
}

// Replaces Go reserved keywords to avoid compilation issues
func replaceAttrReservedWords(identifier string) string {
	value := reservedWordsInAttr[identifier]
	if value != "" {
		return value
	}
	return normalize(identifier)
}

// Normalizes value to be used as a valid Go identifier, avoiding compilation issues
func normalize(value string) string {
	mapping := func(r rune) rune {
		if r == '.' {
			return '_'
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			return r
		}
		return -1
	}

	return strings.Map(mapping, value)
}

func goString(s string) string {
	return strings.Replace(s, "\"", "\\\"", -1)
}

var xsd2GoTypes = map[string]string{
	"string":        "string",
	"token":         "string",
	"float":         "float32",
	"double":        "float64",
	"decimal":       "float64",
	"integer":       "int32",
	"int":           "int32",
	"short":         "int16",
	"byte":          "int8",
	"long":          "int64",
	"boolean":       "bool",
	"datetime":      "soap.XSDDateTime",
	"date":          "soap.XSDDate",
	"time":          "soap.XSDTime",
	"base64binary":  "[]byte",
	"hexbinary":     "[]byte",
	"unsignedint":   "uint32",
	"unsignedshort": "uint16",
	"unsignedbyte":  "byte",
	"unsignedlong":  "uint64",
	"anytype":       "soap.AnyType",
	"ncname":        "soap.NCName",
	"anyuri":        "soap.AnyURI",
	"qname":         "soap.QName",
}

func removeNS(xsdType string) string {
	// Handles name space, ie. xsd:string, xs:string
	r := strings.Split(xsdType, ":")

	if len(r) == 2 {
		return r[1]
	}

	return r[0]
}

func removePointerFromType(goType string) string {
	return regexp.MustCompile("^\\s*\\*").ReplaceAllLiteralString(goType, "")
}

// TODO(c4milo): Add support for namespaces instead of striping them out
// TODO(c4milo): improve runtime complexity if performance turns out to be an issue.
func (g *GoWSDL) findSOAPAction(operation, portType string) string {
	for _, binding := range g.wsdl.Binding {
		if strings.ToUpper(stripns(binding.Type)) != strings.ToUpper(portType) {
			continue
		}

		for _, soapOp := range binding.Operations {
			if soapOp.Name == operation {
				return soapOp.SOAPOperation.SOAPAction
			}
		}
	}
	return ""
}

func (g *GoWSDL) findServiceAddress(name string) string {
	for _, service := range g.wsdl.Service {
		for _, port := range service.Ports {
			if port.Name == name {
				return port.SOAPAddress.Location
			}
		}
	}
	return ""
}

// TODO(c4milo): Add namespace support instead of stripping it
func stripns(xsdType string) string {
	r := strings.Split(xsdType, ":")
	t := r[0]

	if len(r) == 2 {
		t = r[1]
	}

	return t
}

func makePublic(identifier string) string {
	if isBasicType(identifier) {
		return identifier
	}
	if identifier == "" {
		return "EmptyString"
	}
	field := []rune(identifier)
	if len(field) == 0 {
		return identifier
	}

	field[0] = unicode.ToUpper(field[0])
	return string(field)
}

var basicTypes = map[string]string{
	"string":      "string",
	"float32":     "float32",
	"float64":     "float64",
	"int":         "int",
	"int8":        "int8",
	"int16":       "int16",
	"int32":       "int32",
	"int64":       "int64",
	"bool":        "bool",
	"time.Time":   "time.Time",
	"[]byte":      "[]byte",
	"byte":        "byte",
	"uint16":      "uint16",
	"uint32":      "uint32",
	"uinit64":     "uint64",
	"interface{}": "interface{}",
}

func isBasicType(identifier string) bool {
	if _, exists := basicTypes[identifier]; exists {
		return true
	}
	return false
}

func makePrivate(identifier string) string {
	field := []rune(identifier)
	if len(field) == 0 {
		return identifier
	}

	field[0] = unicode.ToLower(field[0])
	return string(field)
}

func comment(text string) string {
	lines := strings.Split(text, "\n")

	var output string
	if len(lines) == 1 && lines[0] == "" {
		return ""
	}

	// Helps to determine if there is an actual comment without screwing newlines
	// in real comments.
	hasComment := false

	for _, line := range lines {
		line = strings.TrimLeftFunc(line, unicode.IsSpace)
		if line != "" {
			hasComment = true
		}
		output += "\n// " + line
	}

	if hasComment {
		return output
	}
	return ""
}

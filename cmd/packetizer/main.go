package main

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Tnze/go-mc/net/packet"
	"golang.org/x/tools/go/packages"
)

type FieldInfo struct {
	Name            string
	Type            string
	NeedConvert     bool
	ValidField      bool
	IsSlice         bool
	IsUnconvertible bool
	IsFunc          bool
}

type StructInfo struct {
	Name    string
	Package string
	Fields  []FieldInfo
	Imports []string
}

type PackageInfo struct {
	File       *ast.File
	Pkg        *packages.Package
	CommentMap ast.CommentMap

	Name string
	Path string

	Structs []StructInfo
	Imports []string
}

func parseTag(tag string) string {
	if tag == "" {
		return ""
	}

	tag = strings.Trim(tag, "`")

	parts := strings.Split(tag, " ")
	for _, part := range parts {
		if strings.HasPrefix(part, "mc:") {
			mcValue := strings.Trim(part[3:], `"`)
			return mcValue
		}
	}

	return ""
}

func shouldProcessStruct(commentMap ast.CommentMap, genDecl *ast.GenDecl) bool {
	if groups, ok := commentMap[genDecl]; ok {
		for _, cg := range groups {
			for _, c := range cg.List {
				content := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(c.Text), "//"))
				if content == "codec:gen" {
					return true
				}
			}
		}
	}
	return false
}

func analyzeFile(pkgInfo *PackageInfo) {
	var pkField *types.Interface
	typ, ok := packetFieldMap["Field"]
	if ok {
		pkField = typ.Underlying().(*types.Interface)
	}
	for _, decl := range pkgInfo.File.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		if !shouldProcessStruct(pkgInfo.CommentMap, gen) {
			continue
		}

		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}

			structInfo := StructInfo{
				Name:    ts.Name.Name,
				Package: pkgInfo.Pkg.Name,
				Fields:  []FieldInfo{},
			}

			for _, field := range st.Fields.List {
				if len(field.Names) == 0 {
					continue
				}

				for _, name := range field.Names {
					fi := FieldInfo{
						Name: name.Name,
					}

					mcTag := parseTag(fieldTagValue(field))
					if mcTag == "-" {
						continue
					}
					obj := pkgInfo.Pkg.TypesInfo.Defs[name]
					if obj != nil {
						t := obj.Type()

						if s, ok := t.(*types.Slice); ok {
							fi.IsSlice = true
							t = s.Elem()
							if mcTag != "" {
								if fieldType, ok := packetFieldMap[mcTag]; ok {
									if _, ok := fieldType.Underlying().(*types.Slice); ok {
										fi.IsSlice = false
										fi.NeedConvert = true
										t = fieldType
									}
								}
							}
						}

						if b, ok := t.(*types.Basic); ok {
							basicType := getBasicType(b.Kind())
							if basicType != nil {
								fi.NeedConvert = true
								t = basicType
							}
						}

						typeString := types.TypeString(t, func(p *types.Package) string {
							return ""
						})
						fi.Type = typeString

						if mcTag != "" {
							if fieldType, ok := packetFieldMap[mcTag]; ok && types.ConvertibleTo(t, fieldType) {
								fi.NeedConvert = true
								fi.Type = mcTag
							}
							if fieldType, ok := packetFieldMap[mcTag]; ok {
								if sig, ok := fieldType.Underlying().(*types.Signature); ok {
									if sig.Results().Len() == 1 {
										fi.IsFunc = true
										fi.Type = mcTag
										t = sig.Results().At(0).Type()
									}
								}
							}
						}

						{
							if types.Implements(t, pkField) || types.Implements(types.NewPointer(t), pkField) {
								fi.ValidField = true
							}
						}
					}

					structInfo.Fields = append(structInfo.Fields, fi)
				}
			}

			pkgInfo.Structs = append(pkgInfo.Structs, structInfo)
		}
	}
}

func getBasicType(kind types.BasicKind) types.Type {
	switch kind {
	case types.Bool:
		return packetFieldMap["Boolean"]
	case types.Int:
		return packetFieldMap["Int"]
	case types.Int8:
		return packetFieldMap["Byte"]
	case types.Int16:
		return packetFieldMap["Short"]
	case types.Int32:
		return packetFieldMap["Int"]
	case types.Int64:
		return packetFieldMap["Long"]
	case types.Uint8:
		return packetFieldMap["UnsignedByte"]
	case types.Uint16:
		return packetFieldMap["UnsignedShort"]
	case types.Float32:
		return packetFieldMap["Float"]
	case types.Float64:
		return packetFieldMap["Double"]
	case types.String:
		return packetFieldMap["String"]
	case types.UntypedBool:
		return packetFieldMap["Boolean"]
	case types.UntypedInt:
		return packetFieldMap["Int"]
	case types.UntypedFloat:
		return packetFieldMap["Double"]
	case types.UntypedString:
		return packetFieldMap["String"]
	default:
		return nil
	}
}

func fieldTagValue(f *ast.Field) string {
	if f.Tag != nil {
		return f.Tag.Value
	}
	return ""
}

func generateFieldTarget(field FieldInfo) string {
	pattern := fmt.Sprintf("(&c.%s)", field.Name)
	if field.IsSlice {
		pattern = "pk.Array" + pattern
	} else if field.IsFunc {
		pattern = "pk." + field.Type + pattern
	} else if field.NeedConvert {
		pattern = "(*pk." + field.Type + ")" + pattern
	}
	return pattern
}

// 按包分組
func groupByPackage(packages []*PackageInfo) map[string]*PackageInfo {
	grouped := make(map[string]*PackageInfo)

	for _, pkg := range packages {
		key := filepath.Dir(pkg.Path)
		if existing, ok := grouped[key]; ok {
			existing.Structs = append(existing.Structs, pkg.Structs...)
			for _, imp := range pkg.Imports {
				found := false
				for _, existingImp := range existing.Imports {
					if existingImp == imp {
						found = true
						break
					}
				}
				if !found {
					existing.Imports = append(existing.Imports, imp)
				}
			}
		} else {
			grouped[key] = pkg
		}
	}

	return grouped
}

var packetFieldMap = map[string]types.Type{}

//go:embed codec.go.tmpl
var codecTemplate string

var tmpl *template.Template

func init() {
	_ = packet.Field(nil)

	funcMap := template.FuncMap{
		"generateTarget": generateFieldTarget,
	}

	tmpl = template.Must(template.New("codecs").Funcs(funcMap).Parse(codecTemplate))
}

func main() {
	dir := flag.String("dir", ".", "input directory to search for codec:gen tags")
	flag.Parse()

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedFiles | packages.NeedDeps,
		Fset: token.NewFileSet(),
		Dir:  *dir,
		Env:  os.Environ(),
	}

	pkgs, err := packages.Load(cfg, "github.com/Tnze/go-mc/net/packet", "./...")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to load packet package: %v\n", err)
		os.Exit(1)
	}
	for _, pkg := range pkgs {
		if pkg.PkgPath == "github.com/Tnze/go-mc/net/packet" {
			scope := pkg.Types.Scope()
			for _, name := range scope.Names() {
				packetFieldMap[name] = scope.Lookup(name).Type()
			}
			break
		}
	}

	var infos []*PackageInfo
	for _, pkg := range pkgs {
		if pkg.PkgPath == "github.com/Tnze/go-mc/net/packet" {
			continue
		}
		for _, file := range pkg.Syntax {
			pf := cfg.Fset.Position(file.Pos()).Filename
			if strings.HasSuffix(pf, "codecs.go") {
				continue
			}

			info := &PackageInfo{File: file, Pkg: pkg, CommentMap: ast.NewCommentMap(cfg.Fset, file, file.Comments), Name: pkg.Name, Path: pf}
			analyzeFile(info)
			if info.Structs == nil || len(info.Structs) == 0 {
				continue
			}
			infos = append(infos, info)
		}
	}

	if len(infos) == 0 {
		fmt.Println("No structs found with // codec:gen. Nothing to generate.")
		return
	}

	grouped := groupByPackage(infos)
	fmt.Printf("Processing %d package groups...\n", len(grouped))

	for dirPath, pkgInfo := range grouped {
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, pkgInfo); err != nil {
			fmt.Fprintf(os.Stderr, "Template execution error: %v\n", err)
			continue
		}

		out := filepath.Join(dirPath, "codecs.go")
		if err := os.WriteFile(out, buf.Bytes(), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", out, err)
			continue
		}
		fmt.Printf("Generated %s\n", out)
	}

	fmt.Println("Code generation complete.")
}

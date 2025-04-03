package events

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

//go:embed api.go
var api_go_source string

func AllTeleportEvents() []EventConstant {
	events, _ := ExtractEventConstants(api_go_source)
	return events
}

// EventConstant holds the extracted constant value and its documentation.
type EventConstant struct {
	Name        string
	Description string
}

func AllEventsDescription() string {
	events := AllTeleportEvents()

	var sb strings.Builder
	for _, e := range events {
		fmt.Fprintf(&sb, "%s: %s\n", e.Name, e.Description)
	}
	return sb.String()
}

// ExtractEventConstants parses Go source and returns string constants
// ending in "Event", along with their doc comments.
func ExtractEventConstants(source string) ([]EventConstant, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "", source, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var events []EventConstant

	for _, decl := range node.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}

		for _, spec := range genDecl.Specs {
			valSpec := spec.(*ast.ValueSpec)
			for i, name := range valSpec.Names {
				if strings.HasSuffix(name.Name, "Event") && len(valSpec.Values) > i {
					if basicLit, ok := valSpec.Values[i].(*ast.BasicLit); ok {
						if basicLit.Kind == token.STRING {
							doc := strings.TrimSpace(valSpec.Doc.Text())
							if doc == "" {
								doc = strings.TrimSpace(genDecl.Doc.Text())
							}
							events = append(events, EventConstant{
								Name:        strings.Trim(basicLit.Value, `"`),
								Description: doc,
							})
						}
					}
				}
			}
		}
	}

	return events, nil
}

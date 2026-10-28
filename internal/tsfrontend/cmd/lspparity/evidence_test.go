package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/totoer/goya/internal/lspfixture"
	"github.com/totoer/goya/lsp"
	"github.com/totoer/goya/lsp/protocol"
)

// Every exposed editor preference must have committed evidence consumed by the
// common direct/wire runner. A file existing somewhere is insufficient proof.
func TestPublicPreferenceEvidenceUsesRecordedFixtures(t *testing.T) {
	_, _, inventory, root := committed(t)
	rows := make(map[string]evidence, len(inventory.Items))
	for _, row := range inventory.Items {
		rows[row.ID] = row
	}
	fields := reflect.TypeFor[lsp.EditorPreferences]()
	for i := 0; i < fields.NumField(); i++ {
		name, _, _ := strings.Cut(fields.Field(i).Tag.Get("json"), ",")
		row, ok := rows["preference/"+name]
		if !ok || row.Status != "verified" {
			t.Errorf("public preference %s lacks verified evidence", name)
			continue
		}
		for label, links := range map[string][]string{"direct": row.DirectTests, "wire": row.ProtocolTests} {
			found := false
			for _, link := range links {
				path, _, _ := strings.Cut(link, "#")
				if !strings.HasPrefix(path, "testdata/lsp/") || filepath.Ext(path) != ".json" {
					continue
				}
				if filepath.Dir(path) != "testdata/lsp" {
					t.Errorf("fixture not included by common runner: %s", path)
					continue
				}
				file, err := os.Open(filepath.Join(root, path))
				if err != nil {
					t.Fatal(err)
				}
				fixture, err := lspfixture.Load(file)
				file.Close()
				if err != nil {
					t.Fatalf("%s: %v", path, err)
				}
				if fixture.Oracle.ServerVersion != pins.TLS.Version || fixture.Oracle.TypeScriptVersion != pins.Oracle.Version {
					t.Errorf("unversioned preference evidence: %s", path)
				}
				if fixture.Initialization != nil {
					if prefs, ok := fixture.Initialization.Options["preferences"].(map[string]any); ok {
						if _, ok := prefs[name]; ok {
							found = true
						}
					}
				}
			}
			if !found {
				t.Errorf("%s preference %s lacks explicit recorded variant", label, name)
			}
		}
	}
}

// Every owned format field needs explicit oracle-backed variants.
func TestPublicFormatSettingsEvidenceUsesRecordedVariants(t *testing.T) {
	_, _, inventory, root := committed(t)
	rows := map[string]evidence{}
	for _, row := range inventory.Items {
		rows[row.ID] = row
	}
	fields := reflect.TypeFor[protocol.FormatCodeSettings]()
	for i := 0; i < fields.NumField(); i++ {
		field := fields.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		row, ok := rows["configuration/typescript.format."+name]
		if !ok || row.Status != "verified" {
			t.Errorf("format field %s lacks verified evidence", name)
			continue
		}
		for kind, links := range map[string][]string{"direct": row.DirectTests, "wire": row.ProtocolTests} {
			values := map[string]bool{}
			for _, link := range links {
				path, _, _ := strings.Cut(link, "#")
				if !strings.HasPrefix(path, "testdata/lsp/") || filepath.Ext(path) != ".json" {
					continue
				}
				file, err := os.Open(filepath.Join(root, path))
				if err != nil {
					t.Fatal(err)
				}
				fixture, err := lspfixture.Load(file)
				file.Close()
				if err != nil {
					t.Fatal(err)
				}
				for _, step := range fixture.Steps {
					if step.Method != "workspace/didChangeConfiguration" {
						continue
					}
					var params struct {
						Settings struct {
							TypeScript struct{ Format map[string]any }
						}
					}
					if err = json.Unmarshal(step.Params, &params); err != nil {
						t.Fatal(err)
					}
					if value, ok := params.Settings.TypeScript.Format[name]; ok {
						encoded, _ := json.Marshal(value)
						values[string(encoded)] = true
					}
				}
			}
			if len(values) < 2 {
				t.Errorf("%s field %s lacks explicit independent variants", kind, name)
			}
			if field.Type.Elem().Kind() == reflect.Bool && (!values["true"] || !values["false"]) {
				t.Errorf("%s field %s lacks true/false variants", kind, name)
			}
			if name == "indentStyle" && (!values["0"] || !values["1"] || !values["2"]) {
				t.Errorf("%s missing indent-style variant", kind)
			}
			if name == "semicolons" && (!values[`"ignore"`] || !values[`"insert"`] || !values[`"remove"`]) {
				t.Errorf("%s missing semicolon variant", kind)
			}
		}
	}
}

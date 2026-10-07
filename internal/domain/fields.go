package domain

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed field_catalog.json
var catalogBytes []byte

type ReferenceSource struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}
type FieldDefinition struct {
	Key                   string   `json:"key"`
	Label                 string   `json:"label"`
	KanoPaths             []string `json:"kano_paths"`
	SourceFile            string   `json:"source_file"`
	AccountSpecific       bool     `json:"account_specific"`
	EnabledForRuns        bool     `json:"enabled_for_runs"`
	PublicMappingVerified bool     `json:"public_v2_mapping_verified"`
	Note                  string   `json:"note"`
}
type FieldCatalog struct {
	SchemaVersion     int               `json:"schema_version"`
	Source            ReferenceSource   `json:"source"`
	ObservedTransport string            `json:"observed_transport"`
	Fields            []FieldDefinition `json:"fields"`
}

func Catalog() (FieldCatalog, error) {
	var c FieldCatalog
	err := json.Unmarshal(catalogBytes, &c)
	if err != nil {
		return c, err
	}
	seen := map[string]bool{}
	for _, f := range c.Fields {
		if seen[f.Key] {
			return c, fmt.Errorf("duplicate field key")
		}
		seen[f.Key] = true
	}
	return c, nil
}

func RunField(key string) bool {
	catalog, err := Catalog()
	if err != nil {
		return false
	}
	for _, f := range catalog.Fields {
		if f.Key == key {
			return f.EnabledForRuns
		}
	}
	return false
}
func FieldLabel(key string) string {
	catalog, _ := Catalog()
	for _, f := range catalog.Fields {
		if f.Key == key {
			return f.Label
		}
	}
	return key
}

package tenantcatalog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	APIVersion       = "noebs.sd/tenants/v1"
	reservedTenantID = "default"
)

var (
	ErrInvalidCatalog  = errors.New("invalid tenant catalog")
	ErrInvalidTenantID = errors.New("invalid tenant ID")
	ErrUnknownTenant   = errors.New("unknown tenant")

	tenantIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
)

type ID string

type Tenant struct {
	ID   ID     `yaml:"id"`
	Name string `yaml:"name"`
}

type Catalog struct {
	tenants []Tenant
	byID    map[ID]Tenant
}

type document struct {
	APIVersion string   `yaml:"api_version"`
	Tenants    []Tenant `yaml:"tenants"`
}

func LoadFile(path string) (Catalog, error) {
	file, err := os.Open(path)
	if err != nil {
		return Catalog{}, fmt.Errorf("open tenant catalog: %w", err)
	}
	catalog, err := Load(file)
	if closeErr := file.Close(); closeErr != nil && err == nil {
		return Catalog{}, fmt.Errorf("close tenant catalog: %w", closeErr)
	}
	return catalog, err
}

func Load(reader io.Reader) (Catalog, error) {
	if reader == nil {
		return Catalog{}, fmt.Errorf("%w: reader is nil", ErrInvalidCatalog)
	}
	var raw document
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return Catalog{}, fmt.Errorf("%w: %v", ErrInvalidCatalog, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Catalog{}, fmt.Errorf("%w: multiple YAML documents", ErrInvalidCatalog)
		}
		return Catalog{}, fmt.Errorf("%w: %v", ErrInvalidCatalog, err)
	}
	if raw.APIVersion != APIVersion {
		return Catalog{}, fmt.Errorf("%w: api_version must be %q", ErrInvalidCatalog, APIVersion)
	}
	return New(raw.Tenants)
}

func New(tenants []Tenant) (Catalog, error) {
	if len(tenants) == 0 {
		return Catalog{}, fmt.Errorf("%w: tenants are required", ErrInvalidCatalog)
	}
	catalog := Catalog{
		tenants: make([]Tenant, 0, len(tenants)),
		byID:    make(map[ID]Tenant, len(tenants)),
	}
	var previous ID
	for index, tenant := range tenants {
		id, err := ParseID(string(tenant.ID))
		if err != nil {
			return Catalog{}, fmt.Errorf("%w: tenants[%d]: %v", ErrInvalidCatalog, index, err)
		}
		if tenant.Name == "" || tenant.Name != strings.TrimSpace(tenant.Name) {
			return Catalog{}, fmt.Errorf("%w: tenants[%d].name must be normalized", ErrInvalidCatalog, index)
		}
		if previous != "" && id <= previous {
			return Catalog{}, fmt.Errorf("%w: tenants must be ordered by unique ID", ErrInvalidCatalog)
		}
		tenant.ID = id
		catalog.tenants = append(catalog.tenants, tenant)
		catalog.byID[id] = tenant
		previous = id
	}
	return catalog, nil
}

func ParseID(raw string) (ID, error) {
	if len(raw) == 0 || len(raw) > 63 || raw == reservedTenantID || !tenantIDPattern.MatchString(raw) {
		return "", fmt.Errorf("%w: %q", ErrInvalidTenantID, raw)
	}
	return ID(raw), nil
}

func (c Catalog) Require(raw string) (Tenant, error) {
	id, err := ParseID(raw)
	if err != nil {
		return Tenant{}, err
	}
	tenant, ok := c.byID[id]
	if !ok {
		return Tenant{}, fmt.Errorf("%w: %s", ErrUnknownTenant, id)
	}
	return tenant, nil
}

func (c Catalog) All() []Tenant {
	return slices.Clone(c.tenants)
}

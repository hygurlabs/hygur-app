package plugin

import (
	"context"
	"reflect"
	"testing"

	"github.com/rs/zerolog"
)

// localityConn is a minimal Connector that only declares a locality.
type localityConn struct {
	id  string
	loc Locality
}

func (c *localityConn) Info() ConnectorInfo                         { return ConnectorInfo{ID: c.id, Name: c.id} }
func (c *localityConn) Capabilities() Capabilities                  { return Capabilities{Locality: c.loc} }
func (c *localityConn) ConfigSchema() ConfigSchema                  { return ConfigSchema{} }
func (c *localityConn) Init(context.Context, ConnectorConfig) error { return nil }
func (c *localityConn) Start(context.Context) error                 { return nil }
func (c *localityConn) Stop(context.Context) error                  { return nil }
func (c *localityConn) Health() HealthStatus                        { return HealthStatus{Status: StatusHealthy} }

// TestConnectorIDsByLocality: connectors partition by declared locality; an unset
// locality counts as server (historical default).
func TestConnectorIDsByLocality(t *testing.T) {
	m := NewManager(nil, zerolog.Nop())
	_ = m.Register(&localityConn{id: "files", loc: LocalityDevice})
	_ = m.Register(&localityConn{id: "notes", loc: LocalityDevice})
	_ = m.Register(&localityConn{id: "gmail", loc: LocalityServer})
	_ = m.Register(&localityConn{id: "legacy", loc: ""}) // unset → server

	if got := m.ConnectorIDsByLocality(LocalityDevice); !reflect.DeepEqual(got, []string{"files", "notes"}) {
		t.Errorf("device = %v, want [files notes]", got)
	}
	if got := m.ConnectorIDsByLocality(LocalityServer); !reflect.DeepEqual(got, []string{"gmail", "legacy"}) {
		t.Errorf("server = %v, want [gmail legacy] (unset counts as server)", got)
	}
}

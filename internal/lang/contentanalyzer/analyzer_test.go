package contentanalyzer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/12ya/reporavel/internal/graph"
	"github.com/12ya/reporavel/internal/scan"
)

func TestMarkdownExtractsSectionsAndLinks(t *testing.T) {
	file := testFile(t, "guide.md", "# Guide\nSee [API](api.md).\n## Setup\n")
	result, err := Markdown().Analyze(context.Background(), "", []scan.File{file})
	if err != nil {
		t.Fatal(err)
	}
	if countKind(result.Nodes, graph.NodeSection) != 2 || countEdges(result.Edges, graph.EdgeCites) != 1 {
		t.Fatalf("nodes=%#v edges=%#v", result.Nodes, result.Edges)
	}
}

func TestMarkdownIgnoresFencedExamplesAndNonHeadings(t *testing.T) {
	file := testFile(t, "guide.md", "# Guide\n```md\n# Example\n[Fake](fake.md)\n```\n#include <stdio.h>\n~~~\n## Also fake\n~~~\n")
	result, err := Markdown().Analyze(context.Background(), "", []scan.File{file})
	if err != nil {
		t.Fatal(err)
	}
	if countKind(result.Nodes, graph.NodeSection) != 1 || countEdges(result.Edges, graph.EdgeCites) != 0 {
		t.Fatalf("nodes=%#v edges=%#v", result.Nodes, result.Edges)
	}
}

func TestSQLExtractsTablesAndColumns(t *testing.T) {
	file := testFile(t, "schema.sql", "CREATE TABLE users (\n id UUID PRIMARY KEY,\n email TEXT NOT NULL\n);\n")
	result, err := SQL().Analyze(context.Background(), "", []scan.File{file})
	if err != nil {
		t.Fatal(err)
	}
	if countKind(result.Nodes, graph.NodeTable) != 1 || countKind(result.Nodes, graph.NodeColumn) != 2 {
		t.Fatalf("nodes=%#v", result.Nodes)
	}
}

func TestSQLStopsColumnsAtTableEnd(t *testing.T) {
	file := testFile(t, "schema.sql", "CREATE TABLE users (\n id UUID\n);\nCREATE INDEX users_id ON users(id);\n")
	result, err := SQL().Analyze(context.Background(), "", []scan.File{file})
	if err != nil {
		t.Fatal(err)
	}
	if countKind(result.Nodes, graph.NodeColumn) != 1 {
		t.Fatalf("nodes=%#v", result.Nodes)
	}
}

func TestSQLResolvesForeignKeysAcrossFilesWithoutConstraintColumns(t *testing.T) {
	users := testFile(t, "db/01_users.sql", "CREATE TABLE users (\n id UUID PRIMARY KEY,\n email TEXT NOT NULL\n);\n")
	accounts := testFile(t, "db/02_accounts.sql", "CREATE TABLE accounts (\n id UUID PRIMARY KEY\n);\n")
	orders := testFile(t, "db/03_orders.sql", "CREATE TABLE orders (\n id UUID PRIMARY KEY,\n user_id UUID REFERENCES users(id),\n account_id UUID,\n CONSTRAINT orders_account_fk FOREIGN KEY (account_id) REFERENCES accounts(id),\n PRIMARY KEY (id),\n UNIQUE (user_id),\n CHECK (account_id IS NOT NULL)\n);\n")

	result, err := SQL().Analyze(context.Background(), "", []scan.File{orders, users, accounts})
	if err != nil {
		t.Fatal(err)
	}
	if got := countKind(result.Nodes, graph.NodeTable); got != 3 {
		t.Fatalf("table nodes = %d, want 3: %#v", got, result.Nodes)
	}
	if got := countKind(result.Nodes, graph.NodeColumn); got != 6 {
		t.Fatalf("column nodes = %d, want 6 (constraints must not become columns): %#v", got, result.Nodes)
	}

	ordersID := graph.ContentID("table", "db", "orders")
	usersID := graph.ContentID("table", "db", "users")
	accountsID := graph.ContentID("table", "db", "accounts")
	assertSQLReference(t, result.Edges, ordersID, usersID, "foreign_key", "db/03_orders.sql:3")
	assertSQLReference(t, result.Edges, ordersID, accountsID, "foreign_key", "db/03_orders.sql:5")
	assertSQLReference(t, result.Edges,
		graph.ContentID("column", ordersID, "user_id"),
		graph.ContentID("column", usersID, "id"),
		"foreign_key", "db/03_orders.sql:3")
	assertSQLReference(t, result.Edges,
		graph.ContentID("column", ordersID, "account_id"),
		graph.ContentID("column", accountsID, "id"),
		"foreign_key", "db/03_orders.sql:5")
}

func TestSQLExtractsViewsIndexesAndCrossFileReferences(t *testing.T) {
	users := testFile(t, "schema/users.sql", "CREATE TABLE public.users (\n id UUID PRIMARY KEY,\n email TEXT\n);\nCREATE UNIQUE INDEX users_email_idx ON public.users(email);\n")
	orders := testFile(t, "schema/orders.sql", "CREATE TABLE orders (\n id UUID PRIMARY KEY,\n user_id UUID\n);\n")
	view := testFile(t, "schema/views.sql", "CREATE MATERIALIZED VIEW user_orders AS\nSELECT u.id, o.id AS order_id\nFROM users AS u\nJOIN orders AS o ON o.user_id = u.id;\n")

	result, err := SQL().Analyze(context.Background(), "", []scan.File{view, orders, users})
	if err != nil {
		t.Fatal(err)
	}
	if got := countKind(result.Nodes, graph.NodeView); got != 1 {
		t.Fatalf("view nodes = %d, want 1: %#v", got, result.Nodes)
	}
	if got := countKind(result.Nodes, graph.NodeIndex); got != 1 {
		t.Fatalf("index nodes = %d, want 1: %#v", got, result.Nodes)
	}

	viewID := graph.ContentID("view", "schema", "user_orders")
	usersID := graph.ContentID("table", "schema", "public.users")
	ordersID := graph.ContentID("table", "schema", "orders")
	assertSQLReference(t, result.Edges, viewID, usersID, "from", "schema/views.sql:3")
	assertSQLReference(t, result.Edges, viewID, ordersID, "join", "schema/views.sql:4")

	viewNode := nodeByID(result.Nodes, viewID)
	if viewNode == nil || viewNode.Meta["materialized"] != "true" || viewNode.Meta["confidence"] != "extracted" {
		t.Fatalf("materialized view metadata = %#v", viewNode)
	}
	indexID := graph.ContentID("index", "schema", "users_email_idx")
	indexNode := nodeByID(result.Nodes, indexID)
	if indexNode == nil || indexNode.Meta["unique"] != "true" || indexNode.Meta["evidence"] != "schema/users.sql:5" {
		t.Fatalf("index metadata = %#v", indexNode)
	}
	if !hasEdge(result.Edges, graph.EdgeContains, usersID, indexID) {
		t.Fatalf("missing table-to-index containment: %#v", result.Edges)
	}
}

func TestSQLIDsAreStableWithinDirectoryAndIsolatedAcrossDirectories(t *testing.T) {
	first := testFile(t, "db/001_users.sql", "CREATE TABLE users (id UUID);\n")
	second := testFile(t, "archive/001_users.sql", "CREATE TABLE users (id UUID);\n")

	result, err := SQL().Analyze(context.Background(), "", []scan.File{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if nodeByID(result.Nodes, graph.ContentID("table", "db", "users")) == nil {
		t.Fatalf("missing directory-scoped db table: %#v", result.Nodes)
	}
	if nodeByID(result.Nodes, graph.ContentID("table", "archive", "users")) == nil {
		t.Fatalf("missing directory-scoped archive table: %#v", result.Nodes)
	}

	renamed := testFile(t, "db/999_renamed.sql", "CREATE TABLE users (id UUID);\n")
	renamedResult, err := SQL().Analyze(context.Background(), "", []scan.File{renamed})
	if err != nil {
		t.Fatal(err)
	}
	if nodeByID(renamedResult.Nodes, graph.ContentID("table", "db", "users")) == nil {
		t.Fatalf("table ID changed when declaration moved within its schema directory: %#v", renamedResult.Nodes)
	}
}

func TestSQLResolvesAlterTableForeignKeyAndIgnoresCTEAlias(t *testing.T) {
	tables := testFile(t, "db/tables.sql", "CREATE TABLE users (id UUID PRIMARY KEY);\nCREATE TABLE events (user_id UUID);\n")
	alter := testFile(t, "db/constraints.sql", "ALTER TABLE events\n ADD CONSTRAINT events_user_fk FOREIGN KEY (user_id) REFERENCES users(id);\n")
	view := testFile(t, "db/view.sql", "CREATE VIEW recent_events AS\nWITH recent AS (SELECT * FROM events)\nSELECT * FROM recent\nJOIN users ON users.id = recent.user_id;\n")

	result, err := SQL().Analyze(context.Background(), "", []scan.File{view, alter, tables})
	if err != nil {
		t.Fatal(err)
	}
	eventsID := graph.ContentID("table", "db", "events")
	usersID := graph.ContentID("table", "db", "users")
	viewID := graph.ContentID("view", "db", "recent_events")
	assertSQLReference(t, result.Edges, eventsID, usersID, "foreign_key", "db/constraints.sql:2")
	assertSQLReference(t, result.Edges, viewID, eventsID, "from", "db/view.sql:2")
	assertSQLReference(t, result.Edges, viewID, usersID, "join", "db/view.sql:4")
	for _, edge := range result.Edges {
		if edge.Kind == graph.EdgeReferences && edge.From == viewID && edge.To == viewID {
			t.Fatalf("CTE alias produced a self-reference: %#v", edge)
		}
	}
}

func testFile(t *testing.T, name, content string) scan.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return scan.File{Path: name, AbsPath: path}
}

func countKind(nodes []graph.Node, kind graph.NodeKind) int {
	count := 0
	for _, node := range nodes {
		if node.Kind == kind {
			count++
		}
	}
	return count
}

func countEdges(edges []graph.Edge, kind graph.EdgeKind) int {
	count := 0
	for _, edge := range edges {
		if edge.Kind == kind {
			count++
		}
	}
	return count
}

func nodeByID(nodes []graph.Node, id string) *graph.Node {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}

func hasEdge(edges []graph.Edge, kind graph.EdgeKind, from, to string) bool {
	for _, edge := range edges {
		if edge.Kind == kind && edge.From == from && edge.To == to {
			return true
		}
	}
	return false
}

func assertSQLReference(t *testing.T, edges []graph.Edge, from, to, relation, evidence string) {
	t.Helper()
	for _, edge := range edges {
		if edge.Kind != graph.EdgeReferences || edge.From != from || edge.To != to || edge.Meta["sqlRelation"] != relation {
			continue
		}
		if edge.Meta["confidence"] != "extracted" || edge.Meta["evidence"] != evidence {
			t.Fatalf("reference %s -> %s metadata = %#v, want extracted evidence %q", from, to, edge.Meta, evidence)
		}
		return
	}
	t.Fatalf("missing %s reference %s -> %s; edges=%#v", relation, from, to, edges)
}

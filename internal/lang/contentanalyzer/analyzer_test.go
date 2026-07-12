package contentanalyzer

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/scan"
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
	indexID := graph.ContentID("index", "schema", "users_email_idx", "public.users")
	indexNode := nodeByID(result.Nodes, indexID)
	if indexNode == nil || indexNode.Meta["unique"] != "true" || indexNode.Meta["evidence"] != "schema/users.sql:5" {
		t.Fatalf("index metadata = %#v", indexNode)
	}
	if !hasEdge(result.Edges, graph.EdgeContains, usersID, indexID) {
		t.Fatalf("missing table-to-index containment: %#v", result.Edges)
	}
}

func TestSQLKeepsSameNamedIndexesOnDifferentTables(t *testing.T) {
	file := testFile(t, "db/indexes.sql", `CREATE TABLE public.users (id UUID);
CREATE TABLE audit.users (id UUID);
CREATE INDEX users_id_idx ON public.users(id);
CREATE INDEX users_id_idx ON audit.users(id);
`)
	result, err := SQL().Analyze(context.Background(), "", []scan.File{file})
	if err != nil {
		t.Fatal(err)
	}
	if got := countKind(result.Nodes, graph.NodeIndex); got != 2 {
		t.Fatalf("same-named per-table indexes collapsed: got %d nodes: %#v", got, result.Nodes)
	}
	for _, table := range []string{"public.users", "audit.users"} {
		tableID := graph.ContentID("table", "db", table)
		indexID := graph.ContentID("index", "db", "users_id_idx", table)
		if nodeByID(result.Nodes, indexID) == nil || !hasEdge(result.Edges, graph.EdgeContains, tableID, indexID) {
			t.Fatalf("missing index %q on %q: nodes=%#v edges=%#v", indexID, tableID, result.Nodes, result.Edges)
		}
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

func TestSQLDoesNotGuessAcrossDirectoryScopes(t *testing.T) {
	dbUsers := testFile(t, "db/users.sql", "CREATE TABLE users (id UUID);\n")
	archiveUsers := testFile(t, "archive/users.sql", "CREATE TABLE users (id UUID);\n")
	dbView := testFile(t, "db/view.sql", "CREATE VIEW active_users AS SELECT * FROM users;\n")
	reportView := testFile(t, "reports/view.sql", "CREATE VIEW active_users AS SELECT * FROM users;\n")

	result, err := SQL().Analyze(context.Background(), "", []scan.File{reportView, archiveUsers, dbView, dbUsers})
	if err != nil {
		t.Fatal(err)
	}
	dbViewID := graph.ContentID("view", "db", "active_users")
	dbUsersID := graph.ContentID("table", "db", "users")
	assertSQLReference(t, result.Edges, dbViewID, dbUsersID, "from", "db/view.sql:1")

	reportViewID := graph.ContentID("view", "reports", "active_users")
	for _, edge := range result.Edges {
		if edge.Kind == graph.EdgeReferences && edge.From == reportViewID {
			t.Fatalf("unscoped reference crossed directories speculatively: %#v", edge)
		}
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

func TestSQLSkipsCommentsLiteralsFunctionsAndAmbiguousReferences(t *testing.T) {
	tables := testFile(t, "db/tables.sql", `-- CREATE TABLE fake (id UUID);
# CREATE TABLE hash_fake (id UUID);
/* outer comment
   /* nested comment */
   CREATE TABLE block_fake (id UUID);
*/
CREATE TABLE public.users (id UUID);
CREATE TABLE audit.users (id UUID);
`)
	views := testFile(t, "db/views.sql", "CREATE VIEW ambiguous_users AS SELECT * FROM users;\nCREATE VIEW public_users AS SELECT * FROM public.users;\nCREATE VIEW literal_only AS SELECT 'FROM public.users' AS message;\nCREATE VIEW generated AS SELECT * FROM generate_series(1, 3);\nCREATE VIEW json_users AS SELECT payload #> '{id}' FROM public.users;\nCREATE VIEW escaped_literal AS SELECT 'don\\'t FROM public.users' AS message;\n")

	result, err := SQL().Analyze(context.Background(), "", []scan.File{views, tables})
	if err != nil {
		t.Fatal(err)
	}
	if got := countKind(result.Nodes, graph.NodeTable); got != 2 {
		t.Fatalf("table nodes = %d, want 2: %#v", got, result.Nodes)
	}
	publicViewID := graph.ContentID("view", "db", "public_users")
	publicUsersID := graph.ContentID("table", "db", "public.users")
	assertSQLReference(t, result.Edges, publicViewID, publicUsersID, "from", "db/views.sql:2")
	assertSQLReference(t, result.Edges, graph.ContentID("view", "db", "json_users"), publicUsersID, "from", "db/views.sql:5")
	for _, viewName := range []string{"ambiguous_users", "literal_only", "generated", "escaped_literal"} {
		viewID := graph.ContentID("view", "db", viewName)
		for _, edge := range result.Edges {
			if edge.Kind == graph.EdgeReferences && edge.From == viewID {
				t.Fatalf("conservative view %s produced a speculative reference: %#v", viewName, edge)
			}
		}
	}
}

func TestSQLKeywordsInsideDelimitedIdentifiersDoNotCreateReferences(t *testing.T) {
	file := testFile(t, "db/quoted.sql", `CREATE TABLE users (id UUID);
CREATE TABLE notes (
  "references users(id)" TEXT,
  marker TEXT,
  typed "my default type" NOT NULL,
  CONSTRAINT "foreign key (marker) references users(id)" CHECK (marker IS NOT NULL)
);
CREATE VIEW "from users" AS SELECT 1;
CREATE VIEW labels AS SELECT "join users" AS label;
`)

	result, err := SQL().Analyze(context.Background(), "", []scan.File{file})
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range result.Edges {
		if edge.Kind == graph.EdgeReferences {
			t.Fatalf("keyword text inside a delimited identifier created a reference: %#v", edge)
		}
	}
	typedColumn := false
	for _, node := range result.Nodes {
		if node.Kind != graph.NodeColumn || node.Name != "typed" {
			continue
		}
		typedColumn = true
		if node.Meta["type"] != `"my default type"` {
			t.Fatalf("delimited type name was truncated at a keyword: %#v", node)
		}
	}
	if !typedColumn {
		t.Fatalf("delimited type column was not extracted: %#v", result.Nodes)
	}
}

func TestSQLDelimitedIdentifiersRemainDistinctAndResolveCompositeForeignKeys(t *testing.T) {
	schema := testFile(t, "db/schema.sql", `CREATE TABLE order.items (id UUID);
CREATE TABLE public."order.items" ("User""ID" UUID PRIMARY KEY);
CREATE VIEW item_view AS SELECT * FROM "order.items";
CREATE TABLE accounts (id UUID);
CREATE TABLE "Accounts" (
  "Tenant""ID" UUID,
  [Account)]]ID] UUID,
  PRIMARY KEY ("Tenant""ID", [Account)]]ID])
);
CREATE TABLE events (tenant_id UUID, account_id UUID);
ALTER TABLE events ADD CONSTRAINT events_account_fk
  FOREIGN KEY (tenant_id, account_id)
  REFERENCES "Accounts" ("Tenant""ID", [Account)]]ID]);
`)

	result, err := SQL().Analyze(context.Background(), "", []scan.File{schema})
	if err != nil {
		t.Fatal(err)
	}
	if got := countKind(result.Nodes, graph.NodeTable); got != 5 {
		t.Fatalf("table nodes = %d, want 5 distinct quoted/unquoted tables: %#v", got, result.Nodes)
	}

	quotedItemsID := graph.ContentID("table", "db", canonicalSQLIdentifier(`public."order.items"`))
	unquotedItemsID := graph.ContentID("table", "db", canonicalSQLIdentifier(`order.items`))
	if quotedItemsID == unquotedItemsID || nodeByID(result.Nodes, quotedItemsID) == nil || nodeByID(result.Nodes, unquotedItemsID) == nil {
		t.Fatalf("quoted and qualified table identifiers collapsed: quoted=%q unquoted=%q nodes=%#v", quotedItemsID, unquotedItemsID, result.Nodes)
	}
	assertSQLReference(t, result.Edges, graph.ContentID("view", "db", "item_view"), quotedItemsID, "from", "db/schema.sql:3")

	quotedAccountsID := graph.ContentID("table", "db", canonicalSQLIdentifier(`"Accounts"`))
	unquotedAccountsID := graph.ContentID("table", "db", "accounts")
	if quotedAccountsID == unquotedAccountsID || nodeByID(result.Nodes, quotedAccountsID) == nil || nodeByID(result.Nodes, unquotedAccountsID) == nil {
		t.Fatalf("case-sensitive delimited table collapsed: quoted=%q unquoted=%q nodes=%#v", quotedAccountsID, unquotedAccountsID, result.Nodes)
	}
	eventsID := graph.ContentID("table", "db", "events")
	assertSQLReference(t, result.Edges, eventsID, quotedAccountsID, "foreign_key", "db/schema.sql:11")
	assertSQLReference(t, result.Edges,
		sqlColumnID(eventsID, "tenant_id"),
		sqlColumnID(quotedAccountsID, canonicalSQLIdentifier(`"Tenant""ID"`)),
		"foreign_key", "db/schema.sql:11")
	assertSQLReference(t, result.Edges,
		sqlColumnID(eventsID, "account_id"),
		sqlColumnID(quotedAccountsID, canonicalSQLIdentifier(`[Account)]]ID]`)),
		"foreign_key", "db/schema.sql:11")
}

func TestSQLCTEColumnListsAndMaterializationDoNotResolveAsTables(t *testing.T) {
	tables := testFile(t, "db/tables.sql", "CREATE TABLE events (id UUID);\nCREATE TABLE recent (id UUID);\nCREATE TABLE archived (id UUID);\n")
	views := testFile(t, "db/views.sql", `CREATE VIEW materialized_events AS
WITH recent(id) AS MATERIALIZED (SELECT id FROM events)
SELECT * FROM recent;
CREATE VIEW unmaterialized_events AS
WITH archived(id) AS NOT MATERIALIZED (SELECT id FROM events)
SELECT * FROM archived;
CREATE VIEW same_named_cte AS
WITH events(id) AS (SELECT id FROM events)
SELECT * FROM events;
CREATE VIEW recursive_cte AS
WITH RECURSIVE events(id) AS (SELECT NULL UNION ALL SELECT id FROM events)
SELECT * FROM events;
`)

	result, err := SQL().Analyze(context.Background(), "", []scan.File{views, tables})
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := SQL().Analyze(context.Background(), "", []scan.File{tables, views})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, reversed) {
		t.Fatalf("SQL analysis changed with input order:\nfirst=%#v\nreversed=%#v", result, reversed)
	}

	eventsID := graph.ContentID("table", "db", "events")
	for _, viewName := range []string{"materialized_events", "unmaterialized_events"} {
		viewID := graph.ContentID("view", "db", viewName)
		if !hasEdge(result.Edges, graph.EdgeReferences, viewID, eventsID) {
			t.Fatalf("CTE-backed view %s omitted its base table: %#v", viewName, result.Edges)
		}
		for _, shadowedTable := range []string{"recent", "archived"} {
			if hasEdge(result.Edges, graph.EdgeReferences, viewID, graph.ContentID("table", "db", shadowedTable)) {
				t.Fatalf("CTE alias %s in view %s resolved as a physical table: %#v", shadowedTable, viewName, result.Edges)
			}
		}
	}
	if !hasEdge(result.Edges, graph.EdgeReferences, graph.ContentID("view", "db", "same_named_cte"), eventsID) {
		t.Fatalf("non-recursive same-named CTE lost its physical base table: %#v", result.Edges)
	}
	if hasEdge(result.Edges, graph.EdgeReferences, graph.ContentID("view", "db", "recursive_cte"), eventsID) {
		t.Fatalf("recursive CTE self-reference resolved as a physical table: %#v", result.Edges)
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

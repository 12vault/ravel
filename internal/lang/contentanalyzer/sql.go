package contentanalyzer

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/lang"
	"github.com/12vault/ravel/internal/scan"
)

const (
	sqlBareIdentifierSegment = `[A-Za-z_][A-Za-z0-9_$]*`
	sqlIdentifierSegment     = `(?:` + sqlBareIdentifierSegment + `|"(?:""|[^"])+"|` + "`(?:``|[^`])+`" + `|\[(?:\]\]|[^\]])+\])`
	sqlIdentifier            = sqlIdentifierSegment + `(?:\s*\.\s*` + sqlIdentifierSegment + `)*`
	sqlIdentifierList        = sqlIdentifierSegment + `(?:\s*,\s*` + sqlIdentifierSegment + `)*`
)

var (
	sqlCreateTableRE = regexp.MustCompile(`(?is)^\s*create\s+(?:temporary\s+|temp\s+)?table\s+(?:if\s+not\s+exists\s+)?(` + sqlIdentifier + `)`)
	sqlCreateViewRE  = regexp.MustCompile(`(?is)^\s*create\s+(?:or\s+replace\s+)?(?:temporary\s+|temp\s+)?(materialized\s+)?view\s+(?:if\s+not\s+exists\s+)?(` + sqlIdentifier + `)`)
	sqlCreateIndexRE = regexp.MustCompile(`(?is)^\s*create\s+(?:(unique)\s+)?index\s+(?:concurrently\s+)?(?:if\s+not\s+exists\s+)?(` + sqlIdentifier + `)\s+on\s+(?:only\s+)?(` + sqlIdentifier + `)`)
	sqlAlterTableRE  = regexp.MustCompile(`(?is)^\s*alter\s+table\s+(?:if\s+exists\s+)?(?:only\s+)?(` + sqlIdentifier + `)`)

	sqlLeadingIdentifierRE = regexp.MustCompile(`^\s*(` + sqlIdentifierSegment + `)`)
	sqlWholeIdentifierRE   = regexp.MustCompile(`^\s*` + sqlIdentifier + `\s*$`)
	sqlColumnBoundaryRE    = regexp.MustCompile(`(?i)\s+(?:constraint|not\s+null|null|default|primary\s+key|unique|references|check|collate|generated|identity)\b`)
	sqlForeignKeyRE        = regexp.MustCompile(`(?is)(?:\bconstraint\s+` + sqlIdentifierSegment + `\s+)?\bforeign\s+key\s*\(\s*(` + sqlIdentifierList + `)\s*\)\s*references\s+(` + sqlIdentifier + `)(?:\s*\(\s*(` + sqlIdentifierList + `)\s*\))?`)
	sqlInlineReferenceRE   = regexp.MustCompile(`(?is)\breferences\s+(` + sqlIdentifier + `)(?:\s*\(\s*(` + sqlIdentifierList + `)\s*\))?`)
	sqlRelationRE          = regexp.MustCompile(`(?is)\b(from|join)\s+(?:(?:only|lateral)\s+)*(` + sqlIdentifier + `)`)
	sqlCTEAliasRE          = regexp.MustCompile(`(?is)(?:\bwith\s+(?:recursive\s+)?|,\s*)(` + sqlIdentifierSegment + `)(?:\s*\(\s*` + sqlIdentifierSegment + `(?:\s*,\s*` + sqlIdentifierSegment + `)*\s*\))?\s+as\s+(?:(?:not\s+)?materialized\s+)?\(`)
	sqlBareIdentifierRE    = regexp.MustCompile(`^` + sqlBareIdentifierSegment + `$`)
)

type sqlSource struct {
	file    scan.File
	content string
}

type sqlStatement struct {
	text string
	line int
}

type sqlColumn struct {
	name      string
	canonical string
	dataType  string
	path      string
	line      int
}

type sqlObject struct {
	id           string
	kind         graph.NodeKind
	name         string
	canonical    string
	scope        string
	path         string
	line         int
	materialized bool
	columns      []sqlColumn
}

type sqlForeignKey struct {
	scope             string
	owner             string
	columns           []string
	target            string
	referencedColumns []string
	path              string
	line              int
}

type sqlReference struct {
	scope    string
	owner    string
	target   string
	relation string
	path     string
	line     int
}

type sqlCTEAlias struct {
	name      string
	nameStart int
	bodyOpen  int
	bodyClose int
	recursive bool
}

type sqlIndex struct {
	id        string
	name      string
	canonical string
	scope     string
	target    string
	unique    bool
	path      string
	line      int
}

type sqlParsedFile struct {
	file        scan.File
	scope       string
	objects     []*sqlObject
	foreignKeys []sqlForeignKey
	references  []sqlReference
	indexes     []sqlIndex
}

type sqlCatalog struct {
	byName  map[string]*sqlObject
	byBase  map[string][]*sqlObject
	objects []*sqlObject
}

func analyzeSQLSources(sources []sqlSource, result *lang.AnalysisResult) {
	sort.Slice(sources, func(i, j int) bool { return sources[i].file.Path < sources[j].file.Path })

	parsed := make([]sqlParsedFile, 0, len(sources))
	catalog := &sqlCatalog{byName: map[string]*sqlObject{}, byBase: map[string][]*sqlObject{}}
	for _, source := range sources {
		file := parseSQLFile(source)
		parsed = append(parsed, file)
		for _, object := range file.objects {
			catalog.add(object)
		}
	}

	emitSQLSchemas(parsed, result)
	catalog.emitNodes(result)
	emitSQLIndexes(parsed, catalog, result)
	emitSQLForeignKeys(parsed, catalog, result)
	emitSQLReferences(parsed, catalog, result)
}

func parseSQLFile(source sqlSource) sqlParsedFile {
	scope := graph.ParentDir(source.file.Path)
	parsed := sqlParsedFile{file: source.file, scope: scope}
	for _, statement := range splitSQLStatements(maskSQLTrivia(source.content)) {
		switch {
		case sqlCreateTableRE.MatchString(statement.text):
			parseSQLTable(statement, source.file.Path, scope, &parsed)
		case sqlCreateViewRE.MatchString(statement.text):
			parseSQLView(statement, source.file.Path, scope, &parsed)
		case sqlCreateIndexRE.MatchString(statement.text):
			parseSQLIndex(statement, source.file.Path, scope, &parsed)
		case sqlAlterTableRE.MatchString(statement.text):
			parseSQLAlterTable(statement, source.file.Path, scope, &parsed)
		}
	}
	return parsed
}

func parseSQLTable(statement sqlStatement, path, scope string, parsed *sqlParsedFile) {
	match := sqlCreateTableRE.FindStringSubmatch(statement.text)
	indices := sqlCreateTableRE.FindStringSubmatchIndex(statement.text)
	if len(match) != 2 || len(indices) < 4 {
		return
	}
	name, canonical := displaySQLIdentifier(match[1]), canonicalSQLIdentifier(match[1])
	if canonical == "" {
		return
	}
	object := &sqlObject{
		id:        graph.ContentID("table", scope, canonical),
		kind:      graph.NodeTable,
		name:      name,
		canonical: canonical,
		scope:     scope,
		path:      path,
		line:      statement.line,
	}
	parsed.objects = append(parsed.objects, object)

	open := nextSQLNonSpace(statement.text, indices[3])
	if open < len(statement.text) && statement.text[open] == '(' {
		if close := matchingSQLParen(statement.text, open); close >= 0 {
			body := statement.text[open+1 : close]
			for _, item := range splitSQLList(body) {
				line := statement.line + strings.Count(statement.text[:open+1+item.offset], "\n")
				parseSQLTableItem(item.text, line, path, object, parsed)
			}
		}
	}
	appendSQLRelations(statement, path, scope, canonical, parsed)
}

func parseSQLTableItem(item string, line int, path string, object *sqlObject, parsed *sqlParsedFile) {
	leading := sqlLeadingIdentifierRE.FindStringSubmatch(item)
	if len(leading) != 2 {
		return
	}
	if !quotedSQLIdentifier(leading[1]) && sqlConstraint(leading[1]) {
		for _, indices := range sqlForeignKeyRE.FindAllStringSubmatchIndex(item, -1) {
			if sqlInsideDelimitedIdentifier(item, indices[0]) {
				continue
			}
			appendSQLForeignKey(submatches(item, indices), object.scope, object.canonical, path, line, parsed)
		}
		return
	}

	column, ok := parseSQLColumn(item, path, line)
	if !ok {
		return
	}
	object.addColumn(column)
	if match := firstSQLSyntaxSubmatch(sqlInlineReferenceRE, item); len(match) == 3 {
		target := canonicalSQLIdentifier(match[1])
		if target != "" {
			parsed.foreignKeys = append(parsed.foreignKeys, sqlForeignKey{
				scope:             object.scope,
				owner:             object.canonical,
				columns:           []string{column.canonical},
				target:            target,
				referencedColumns: parseSQLIdentifierList(match[2]),
				path:              path,
				line:              line,
			})
		}
	}
}

func parseSQLColumn(item, path string, line int) (sqlColumn, bool) {
	indices := sqlLeadingIdentifierRE.FindStringSubmatchIndex(item)
	match := sqlLeadingIdentifierRE.FindStringSubmatch(item)
	if len(match) != 2 || len(indices) < 4 {
		return sqlColumn{}, false
	}
	remainder := strings.TrimSpace(item[indices[3]:])
	if remainder == "" {
		return sqlColumn{}, false
	}
	if boundary := firstSQLSyntaxIndex(sqlColumnBoundaryRE, remainder); boundary >= 0 {
		remainder = strings.TrimSpace(remainder[:boundary])
	}
	if remainder == "" || !validSQLTypeStart(remainder[0]) {
		return sqlColumn{}, false
	}
	canonical := canonicalSQLIdentifier(match[1])
	if canonical == "" {
		return sqlColumn{}, false
	}
	return sqlColumn{
		name:      displaySQLIdentifier(match[1]),
		canonical: canonical,
		dataType:  remainder,
		path:      path,
		line:      line,
	}, true
}

func parseSQLView(statement sqlStatement, path, scope string, parsed *sqlParsedFile) {
	match := sqlCreateViewRE.FindStringSubmatch(statement.text)
	if len(match) != 3 {
		return
	}
	name, canonical := displaySQLIdentifier(match[2]), canonicalSQLIdentifier(match[2])
	if canonical == "" {
		return
	}
	parsed.objects = append(parsed.objects, &sqlObject{
		id:           graph.ContentID("view", scope, canonical),
		kind:         graph.NodeView,
		name:         name,
		canonical:    canonical,
		scope:        scope,
		path:         path,
		line:         statement.line,
		materialized: strings.TrimSpace(match[1]) != "",
	})
	appendSQLRelations(statement, path, scope, canonical, parsed)
}

func parseSQLIndex(statement sqlStatement, path, scope string, parsed *sqlParsedFile) {
	match := sqlCreateIndexRE.FindStringSubmatch(statement.text)
	if len(match) != 4 {
		return
	}
	name, canonical := displaySQLIdentifier(match[2]), canonicalSQLIdentifier(match[2])
	target := canonicalSQLIdentifier(match[3])
	if canonical == "" || target == "" {
		return
	}
	parsed.indexes = append(parsed.indexes, sqlIndex{
		id:        graph.ContentID("index", scope, canonical, target),
		name:      name,
		canonical: canonical,
		scope:     scope,
		target:    target,
		unique:    strings.TrimSpace(match[1]) != "",
		path:      path,
		line:      statement.line,
	})
}

func parseSQLAlterTable(statement sqlStatement, path, scope string, parsed *sqlParsedFile) {
	match := sqlAlterTableRE.FindStringSubmatch(statement.text)
	if len(match) != 2 {
		return
	}
	owner := canonicalSQLIdentifier(match[1])
	if owner == "" {
		return
	}
	for _, foreignKey := range sqlForeignKeyRE.FindAllStringSubmatchIndex(statement.text, -1) {
		if sqlInsideDelimitedIdentifier(statement.text, foreignKey[0]) {
			continue
		}
		groups := submatches(statement.text, foreignKey)
		if len(groups) != 4 {
			continue
		}
		line := statement.line + strings.Count(statement.text[:foreignKey[0]], "\n")
		appendSQLForeignKey(groups, scope, owner, path, line, parsed)
	}
}

func appendSQLForeignKey(match []string, scope, owner, path string, line int, parsed *sqlParsedFile) {
	if len(match) != 4 {
		return
	}
	columns := parseSQLIdentifierList(match[1])
	target := canonicalSQLIdentifier(match[2])
	if len(columns) == 0 || target == "" {
		return
	}
	parsed.foreignKeys = append(parsed.foreignKeys, sqlForeignKey{
		scope:             scope,
		owner:             owner,
		columns:           columns,
		target:            target,
		referencedColumns: parseSQLIdentifierList(match[3]),
		path:              path,
		line:              line,
	})
}

func appendSQLRelations(statement sqlStatement, path, scope, owner string, parsed *sqlParsedFile) {
	cteAliases := findSQLCTEAliases(statement.text)
	for _, indices := range sqlRelationRE.FindAllStringSubmatchIndex(statement.text, -1) {
		if len(indices) < 6 || sqlInsideDelimitedIdentifier(statement.text, indices[0]) {
			continue
		}
		target := canonicalSQLIdentifier(statement.text[indices[4]:indices[5]])
		if target == "" || sqlRelationUsesCTE(target, indices[0], cteAliases) || sqlCallFollows(statement.text, indices[5]) {
			continue
		}
		parsed.references = append(parsed.references, sqlReference{
			scope:    scope,
			owner:    owner,
			target:   target,
			relation: strings.ToLower(statement.text[indices[2]:indices[3]]),
			path:     path,
			line:     statement.line + strings.Count(statement.text[:indices[0]], "\n"),
		})
	}
}

func findSQLCTEAliases(statement string) []sqlCTEAlias {
	matches := sqlCTEAliasRE.FindAllStringSubmatchIndex(statement, -1)
	aliases := make([]sqlCTEAlias, 0, len(matches))
	recursive := false
	for _, indices := range matches {
		if len(indices) < 4 || indices[2] < 0 || indices[3] < 0 || sqlInsideDelimitedIdentifier(statement, indices[0]) {
			continue
		}
		prefix := strings.ToLower(strings.TrimSpace(statement[indices[0]:indices[2]]))
		if strings.HasPrefix(prefix, "with") {
			recursive = strings.Contains(prefix, "recursive")
		}
		bodyOpen := indices[1] - 1
		if bodyOpen < 0 || bodyOpen >= len(statement) || statement[bodyOpen] != '(' {
			continue
		}
		bodyClose := matchingSQLParen(statement, bodyOpen)
		if bodyClose < 0 {
			continue
		}
		name := canonicalSQLIdentifier(statement[indices[2]:indices[3]])
		if name == "" {
			continue
		}
		aliases = append(aliases, sqlCTEAlias{
			name:      name,
			nameStart: indices[2],
			bodyOpen:  bodyOpen,
			bodyClose: bodyClose,
			recursive: recursive,
		})
	}
	return aliases
}

func sqlRelationUsesCTE(target string, relationStart int, aliases []sqlCTEAlias) bool {
	for _, alias := range aliases {
		if alias.name != target {
			continue
		}
		if alias.recursive {
			return true
		}
		// In a non-recursive WITH clause, a CTE is visible only after its
		// definition. A same-named relation inside the definition still
		// names the physical table, while uses after the closing parenthesis
		// name the CTE and must not become graph references.
		if relationStart > alias.bodyClose {
			return true
		}
		if relationStart < alias.nameStart || (relationStart > alias.bodyOpen && relationStart < alias.bodyClose) {
			return false
		}
	}
	return false
}

func emitSQLSchemas(parsed []sqlParsedFile, result *lang.AnalysisResult) {
	seen := map[string]bool{}
	for _, file := range parsed {
		schemaID := graph.ContentID("schema", file.scope)
		if !seen[schemaID] {
			seen[schemaID] = true
			result.Nodes = append(result.Nodes, graph.Node{
				ID:   schemaID,
				Kind: graph.NodeSchema,
				Name: file.scope,
				Path: file.scope,
				Meta: extractedMeta(file.file.Path, 1),
			})
		}
		result.Edges = append(result.Edges, graph.Edge{
			Kind: graph.EdgeDefines,
			From: graph.FileID(file.file.Path),
			To:   schemaID,
			Meta: extractedMeta(file.file.Path, 1),
		})
	}
}

func (catalog *sqlCatalog) add(object *sqlObject) {
	key := sqlCatalogKey(object.scope, object.canonical)
	if existing := catalog.byName[key]; existing != nil {
		for _, column := range object.columns {
			existing.addColumn(column)
		}
		return
	}
	catalog.byName[key] = object
	baseKey := sqlCatalogKey(object.scope, sqlBaseName(object.canonical))
	catalog.byBase[baseKey] = append(catalog.byBase[baseKey], object)
	catalog.objects = append(catalog.objects, object)
}

func (catalog *sqlCatalog) resolve(scope, name string) *sqlObject {
	canonical := canonicalSQLIdentifier(name)
	if canonical == "" {
		return nil
	}
	if object := catalog.byName[sqlCatalogKey(scope, canonical)]; object != nil {
		return object
	}
	candidates := catalog.byBase[sqlCatalogKey(scope, sqlBaseName(canonical))]
	if len(candidates) == 1 && (!qualifiedSQLIdentifier(canonical) || !qualifiedSQLIdentifier(candidates[0].canonical)) {
		return candidates[0]
	}
	return nil
}

func (catalog *sqlCatalog) emitNodes(result *lang.AnalysisResult) {
	objects := append([]*sqlObject(nil), catalog.objects...)
	sort.Slice(objects, func(i, j int) bool { return objects[i].id < objects[j].id })
	for _, object := range objects {
		meta := extractedMeta(object.path, object.line)
		if object.kind == graph.NodeView {
			meta["sqlKind"] = "view"
			if object.materialized {
				meta["materialized"] = "true"
			}
		} else {
			meta["sqlKind"] = "table"
		}
		result.Nodes = append(result.Nodes, graph.Node{
			ID:        object.id,
			Kind:      object.kind,
			Name:      object.name,
			Path:      object.path,
			StartLine: object.line,
			Meta:      meta,
		})
		result.Edges = append(result.Edges, graph.Edge{
			Kind: graph.EdgeContains,
			From: graph.ContentID("schema", object.scope),
			To:   object.id,
			Meta: extractedMeta(object.path, object.line),
		})
		for _, column := range object.columns {
			columnID := sqlColumnID(object.id, column.canonical)
			columnMeta := extractedMeta(column.path, column.line)
			columnMeta["type"] = column.dataType
			result.Nodes = append(result.Nodes, graph.Node{
				ID:        columnID,
				Kind:      graph.NodeColumn,
				Name:      column.name,
				Path:      column.path,
				StartLine: column.line,
				Meta:      columnMeta,
			})
			result.Edges = append(result.Edges, graph.Edge{
				Kind: graph.EdgeContains,
				From: object.id,
				To:   columnID,
				Meta: extractedMeta(column.path, column.line),
			})
		}
	}
}

func emitSQLIndexes(parsed []sqlParsedFile, catalog *sqlCatalog, result *lang.AnalysisResult) {
	indexes := make([]sqlIndex, 0)
	for _, file := range parsed {
		indexes = append(indexes, file.indexes...)
	}
	sort.Slice(indexes, func(i, j int) bool {
		if indexes[i].id == indexes[j].id {
			return indexes[i].path < indexes[j].path
		}
		return indexes[i].id < indexes[j].id
	})
	seen := map[string]bool{}
	for _, index := range indexes {
		if seen[index.id] {
			continue
		}
		seen[index.id] = true
		meta := extractedMeta(index.path, index.line)
		meta["sqlKind"] = "index"
		meta["table"] = index.target
		meta["unique"] = boolString(index.unique)
		result.Nodes = append(result.Nodes, graph.Node{
			ID:        index.id,
			Kind:      graph.NodeIndex,
			Name:      index.name,
			Path:      index.path,
			StartLine: index.line,
			Meta:      meta,
		})
		parent := graph.ContentID("schema", index.scope)
		if table := catalog.resolve(index.scope, index.target); table != nil {
			parent = table.id
		}
		result.Edges = append(result.Edges, graph.Edge{
			Kind: graph.EdgeContains,
			From: parent,
			To:   index.id,
			Meta: extractedMeta(index.path, index.line),
		})
	}
}

func emitSQLForeignKeys(parsed []sqlParsedFile, catalog *sqlCatalog, result *lang.AnalysisResult) {
	seen := map[string]bool{}
	for _, file := range parsed {
		for _, foreignKey := range file.foreignKeys {
			owner := catalog.resolve(foreignKey.scope, foreignKey.owner)
			target := catalog.resolve(foreignKey.scope, foreignKey.target)
			if owner == nil || target == nil {
				continue
			}
			meta := extractedMeta(foreignKey.path, foreignKey.line)
			meta["sqlRelation"] = "foreign_key"
			meta["columns"] = strings.Join(foreignKey.columns, ",")
			if len(foreignKey.referencedColumns) > 0 {
				meta["referencedColumns"] = strings.Join(foreignKey.referencedColumns, ",")
			}
			discriminator := strings.Join(foreignKey.columns, ",") + "->" + strings.Join(foreignKey.referencedColumns, ",")
			edgeID := sqlReferenceEdgeID(owner.id, target.id, "foreign_key", discriminator)
			if !seen[edgeID] {
				seen[edgeID] = true
				result.Edges = append(result.Edges, graph.Edge{ID: edgeID, Kind: graph.EdgeReferences, From: owner.id, To: target.id, Meta: meta})
			}

			for i, sourceName := range foreignKey.columns {
				if i >= len(foreignKey.referencedColumns) {
					break
				}
				sourceColumn := owner.column(sourceName)
				targetColumn := target.column(foreignKey.referencedColumns[i])
				if sourceColumn == nil || targetColumn == nil {
					continue
				}
				sourceID := sqlColumnID(owner.id, sourceColumn.canonical)
				targetID := sqlColumnID(target.id, targetColumn.canonical)
				columnEdgeID := sqlReferenceEdgeID(sourceID, targetID, "foreign_key", "")
				if seen[columnEdgeID] {
					continue
				}
				seen[columnEdgeID] = true
				result.Edges = append(result.Edges, graph.Edge{ID: columnEdgeID, Kind: graph.EdgeReferences, From: sourceID, To: targetID, Meta: meta})
			}
		}
	}
}

func emitSQLReferences(parsed []sqlParsedFile, catalog *sqlCatalog, result *lang.AnalysisResult) {
	seen := map[string]bool{}
	for _, file := range parsed {
		for _, reference := range file.references {
			owner := catalog.resolve(reference.scope, reference.owner)
			target := catalog.resolve(reference.scope, reference.target)
			if owner == nil || target == nil {
				continue
			}
			edgeID := sqlReferenceEdgeID(owner.id, target.id, reference.relation, "")
			if seen[edgeID] {
				continue
			}
			seen[edgeID] = true
			meta := extractedMeta(reference.path, reference.line)
			meta["sqlRelation"] = reference.relation
			result.Edges = append(result.Edges, graph.Edge{ID: edgeID, Kind: graph.EdgeReferences, From: owner.id, To: target.id, Meta: meta})
		}
	}
}

func (object *sqlObject) addColumn(column sqlColumn) {
	if object.column(column.canonical) == nil {
		object.columns = append(object.columns, column)
	}
}

func (object *sqlObject) column(name string) *sqlColumn {
	canonical := canonicalSQLIdentifier(name)
	for i := range object.columns {
		if object.columns[i].canonical == canonical {
			return &object.columns[i]
		}
	}
	return nil
}

func splitSQLStatements(content string) []sqlStatement {
	statements := make([]sqlStatement, 0)
	start, baseLine := 0, 1
	for i := 0; i < len(content); {
		switch content[i] {
		case '"', '`':
			i = skipSQLQuoted(content, i, content[i])
		case '[':
			i = skipSQLBracketed(content, i)
		case ';':
			statements = appendSQLStatement(statements, content[start:i], baseLine)
			baseLine += strings.Count(content[start:i+1], "\n")
			start = i + 1
			i++
		default:
			i++
		}
	}
	return appendSQLStatement(statements, content[start:], baseLine)
}

func appendSQLStatement(statements []sqlStatement, text string, baseLine int) []sqlStatement {
	leading := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
	line := baseLine + strings.Count(text[:leading], "\n")
	text = strings.TrimRightFunc(text[leading:], unicode.IsSpace)
	if text == "" {
		return statements
	}
	return append(statements, sqlStatement{text: text, line: line})
}

func maskSQLTrivia(content string) string {
	masked := []byte(content)
	for i := 0; i < len(content); {
		switch {
		case content[i] == '\'':
			end := skipSQLQuoted(content, i, '\'')
			maskSQLRange(masked, i, end)
			i = end
		case content[i] == '"' || content[i] == '`':
			i = skipSQLQuoted(content, i, content[i])
		case content[i] == '[':
			i = skipSQLBracketed(content, i)
		case content[i] == '-' && i+1 < len(content) && content[i+1] == '-':
			end := strings.IndexByte(content[i:], '\n')
			if end < 0 {
				end = len(content)
			} else {
				end += i
			}
			maskSQLRange(masked, i, end)
			i = end
		case content[i] == '#' && sqlHashStartsComment(content, i):
			end := strings.IndexByte(content[i:], '\n')
			if end < 0 {
				end = len(content)
			} else {
				end += i
			}
			maskSQLRange(masked, i, end)
			i = end
		case content[i] == '/' && i+1 < len(content) && content[i+1] == '*':
			end := skipSQLBlockComment(content, i)
			maskSQLRange(masked, i, end)
			i = end
		case content[i] == '$':
			delimiter, ok := sqlDollarDelimiter(content, i)
			if !ok {
				i++
				continue
			}
			closing := strings.Index(content[i+len(delimiter):], delimiter)
			if closing < 0 {
				i++
				continue
			}
			end := i + len(delimiter) + closing + len(delimiter)
			maskSQLRange(masked, i, end)
			i = end
		default:
			i++
		}
	}
	return string(masked)
}

func sqlHashStartsComment(content string, position int) bool {
	for i := position - 1; i >= 0 && content[i] != '\n' && content[i] != '\r'; i-- {
		if !unicode.IsSpace(rune(content[i])) {
			return false
		}
	}
	return true
}

func skipSQLBlockComment(content string, start int) int {
	depth := 1
	for i := start + 2; i < len(content); {
		switch {
		case i+1 < len(content) && content[i] == '/' && content[i+1] == '*':
			depth++
			i += 2
		case i+1 < len(content) && content[i] == '*' && content[i+1] == '/':
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	return len(content)
}

func maskSQLRange(content []byte, start, end int) {
	for i := start; i < end && i < len(content); i++ {
		if content[i] != '\n' && content[i] != '\r' {
			content[i] = ' '
		}
	}
}

func skipSQLQuoted(content string, start int, quote byte) int {
	for i := start + 1; i < len(content); i++ {
		if content[i] == '\\' && i+1 < len(content) {
			i++
			continue
		}
		if content[i] != quote {
			continue
		}
		if i+1 < len(content) && content[i+1] == quote {
			i++
			continue
		}
		return i + 1
	}
	return len(content)
}

func skipSQLBracketed(content string, start int) int {
	for i := start + 1; i < len(content); i++ {
		if content[i] != ']' {
			continue
		}
		if i+1 < len(content) && content[i+1] == ']' {
			i++
			continue
		}
		return i + 1
	}
	return len(content)
}

func sqlDollarDelimiter(content string, start int) (string, bool) {
	for i := start + 1; i < len(content); i++ {
		if content[i] == '$' {
			return content[start : i+1], true
		}
		if (content[i] < 'A' || content[i] > 'Z') && (content[i] < 'a' || content[i] > 'z') && (content[i] < '0' || content[i] > '9') && content[i] != '_' {
			return "", false
		}
	}
	return "", false
}

type sqlListItem struct {
	text   string
	offset int
}

func splitSQLList(content string) []sqlListItem {
	items := make([]sqlListItem, 0)
	start, depth := 0, 0
	appendItem := func(end int) {
		part := content[start:end]
		leading := len(part) - len(strings.TrimLeftFunc(part, unicode.IsSpace))
		part = strings.TrimSpace(part)
		if part != "" {
			items = append(items, sqlListItem{text: part, offset: start + leading})
		}
	}
	for i := 0; i < len(content); {
		switch content[i] {
		case '"', '`':
			i = skipSQLQuoted(content, i, content[i])
		case '[':
			i = skipSQLBracketed(content, i)
		case '(':
			depth++
			i++
		case ')':
			if depth > 0 {
				depth--
			}
			i++
		case ',':
			if depth == 0 {
				appendItem(i)
				start = i + 1
			}
			i++
		default:
			i++
		}
	}
	appendItem(len(content))
	return items
}

func matchingSQLParen(content string, open int) int {
	depth := 0
	for i := open; i < len(content); {
		switch content[i] {
		case '"', '`':
			i = skipSQLQuoted(content, i, content[i])
		case '[':
			i = skipSQLBracketed(content, i)
		case '(':
			depth++
			i++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
			i++
		default:
			i++
		}
	}
	return -1
}

func nextSQLNonSpace(content string, start int) int {
	for start < len(content) && unicode.IsSpace(rune(content[start])) {
		start++
	}
	return start
}

func parseSQLIdentifierList(content string) []string {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	parts := splitSQLList(content)
	identifiers := make([]string, 0, len(parts))
	for _, part := range parts {
		if !sqlWholeIdentifierRE.MatchString(part.text) {
			return nil
		}
		identifier := canonicalSQLIdentifier(part.text)
		if identifier == "" {
			return nil
		}
		identifiers = append(identifiers, identifier)
	}
	return identifiers
}

func canonicalSQLIdentifier(identifier string) string {
	parts := splitSQLIdentifier(identifier)
	for i := range parts {
		part := strings.TrimSpace(parts[i])
		value := unquoteSQLIdentifier(part)
		if value == "" {
			return ""
		}
		if !quotedSQLIdentifier(part) {
			parts[i] = strings.ToLower(value)
			continue
		}

		// Lower-case delimited names such as "users" refer to the same
		// object as their ordinary spelling in the common SQL dialects Ravel
		// supports. Preserve delimiters for mixed-case or otherwise special
		// names so that "Users" and "order.items" do not collapse into the
		// unquoted identifiers users and order.items.
		if value == strings.ToLower(value) && sqlBareIdentifierRE.MatchString(value) {
			parts[i] = value
			continue
		}
		parts[i] = `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return strings.Join(parts, ".")
}

func displaySQLIdentifier(identifier string) string {
	parts := splitSQLIdentifier(identifier)
	for i := range parts {
		parts[i] = unquoteSQLIdentifier(strings.TrimSpace(parts[i]))
	}
	return strings.Join(parts, ".")
}

func splitSQLIdentifier(identifier string) []string {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil
	}
	parts := make([]string, 0, 2)
	start := 0
	for i := 0; i < len(identifier); {
		switch identifier[i] {
		case '"', '`':
			i = skipSQLQuoted(identifier, i, identifier[i])
		case '[':
			i = skipSQLBracketed(identifier, i)
		case '.':
			parts = append(parts, identifier[start:i])
			start = i + 1
			i++
		default:
			i++
		}
	}
	return append(parts, identifier[start:])
}

func unquoteSQLIdentifier(identifier string) string {
	if len(identifier) < 2 {
		return identifier
	}
	switch {
	case identifier[0] == '"' && identifier[len(identifier)-1] == '"':
		return strings.ReplaceAll(identifier[1:len(identifier)-1], `""`, `"`)
	case identifier[0] == '`' && identifier[len(identifier)-1] == '`':
		return strings.ReplaceAll(identifier[1:len(identifier)-1], "``", "`")
	case identifier[0] == '[' && identifier[len(identifier)-1] == ']':
		return strings.ReplaceAll(identifier[1:len(identifier)-1], "]]", "]")
	default:
		return identifier
	}
}

func quotedSQLIdentifier(identifier string) bool {
	identifier = strings.TrimSpace(identifier)
	return len(identifier) > 1 && (identifier[0] == '"' || identifier[0] == '`' || identifier[0] == '[')
}

func sqlConstraint(value string) bool {
	switch canonicalSQLIdentifier(value) {
	case "primary", "foreign", "unique", "constraint", "check", "key", "exclude", "index", "fulltext", "spatial", "like", "period":
		return true
	default:
		return false
	}
}

func sqlCallFollows(content string, identifierEnd int) bool {
	identifierEnd = nextSQLNonSpace(content, identifierEnd)
	return identifierEnd < len(content) && content[identifierEnd] == '('
}

func firstSQLSyntaxSubmatch(expression *regexp.Regexp, content string) []string {
	for _, indices := range expression.FindAllStringSubmatchIndex(content, -1) {
		if !sqlInsideDelimitedIdentifier(content, indices[0]) {
			return submatches(content, indices)
		}
	}
	return nil
}

func firstSQLSyntaxIndex(expression *regexp.Regexp, content string) int {
	for _, indices := range expression.FindAllStringIndex(content, -1) {
		if !sqlInsideDelimitedIdentifier(content, indices[0]) {
			return indices[0]
		}
	}
	return -1
}

func sqlInsideDelimitedIdentifier(content string, position int) bool {
	for i := 0; i < len(content) && i <= position; {
		start := i
		switch content[i] {
		case '"', '`':
			i = skipSQLQuoted(content, i, content[i])
		case '[':
			i = skipSQLBracketed(content, i)
		default:
			i++
			continue
		}
		if position > start && position < i {
			return true
		}
	}
	return false
}

func sqlCatalogKey(scope, name string) string {
	return scope + "\x00" + name
}

func sqlBaseName(name string) string {
	parts := splitSQLIdentifier(name)
	if len(parts) == 0 {
		return ""
	}
	return canonicalSQLIdentifier(parts[len(parts)-1])
}

func qualifiedSQLIdentifier(name string) bool {
	return len(splitSQLIdentifier(name)) > 1
}

func sqlColumnID(objectID, column string) string {
	return graph.ContentID("column", objectID, canonicalSQLIdentifier(column))
}

func sqlReferenceEdgeID(from, to, relation, discriminator string) string {
	identity := strings.Join([]string{from, to, relation, discriminator}, "\x00")
	sum := sha1.Sum([]byte(identity))
	return "sql-reference://" + hex.EncodeToString(sum[:12])
}

func validSQLTypeStart(value byte) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z') || value == '"' || value == '`' || value == '['
}

func submatches(content string, indices []int) []string {
	if len(indices)%2 != 0 {
		return nil
	}
	matches := make([]string, 0, len(indices)/2)
	for i := 0; i < len(indices); i += 2 {
		if indices[i] < 0 || indices[i+1] < 0 {
			matches = append(matches, "")
			continue
		}
		matches = append(matches, content[indices[i]:indices[i+1]])
	}
	return matches
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

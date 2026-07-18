package query

import (
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/12vault/ravel/internal/graph"
)

const (
	indexCacheFormat       = 1
	indexCachePrefix       = "query-index-v1-"
	indexCacheSuffix       = ".gob"
	maxPersistedIndexBytes = int64(1 << 30)
)

type persistedIndex struct {
	Format            uint32
	GraphHash         [sha256.Size]byte
	Graph             graph.Graph
	Documents         []persistedDocument
	DocumentFrequency map[string]int
	TrigramPostings   map[string][]int
	AverageLength     persistedFieldLengths
}

type persistedDocument struct {
	NameText      string
	PathText      string
	PackageText   string
	IDText        string
	MetaText      string
	NameTerms     map[string]int
	NameTermOrder []string
	PathTerms     map[string]int
	PackageTerms  map[string]int
	IDTerms       map[string]int
	MetaTerms     map[string]int
	Lengths       persistedFieldLengths
}

type persistedFieldLengths struct {
	Name        float64
	Path        float64
	PackageName float64
	ID          float64
	Meta        float64
}

// LoadOrBuildIndex loads a graph-hash-keyed immutable index from cache or
// builds one from graphData. Cache failures are intentionally best-effort:
// malformed or unwritable cache state never prevents a valid graph query.
func LoadOrBuildIndex(graphData []byte, cacheDir string) (*Index, bool, error) {
	graphHash := sha256.Sum256(graphData)
	cachePath := persistedIndexPath(cacheDir, graphHash)
	if cacheDir != "" {
		if index, ok := loadPersistedIndex(cachePath, graphHash); ok {
			return index, true, nil
		}
	}

	var value graph.Graph
	if err := json.Unmarshal(graphData, &value); err != nil {
		return nil, false, fmt.Errorf("decode graph state: %w", err)
	}
	index := NewIndex(value)
	if cacheDir != "" {
		if writePersistedIndex(cachePath, graphHash, index) == nil {
			prunePersistedIndexes(cacheDir, filepath.Base(cachePath))
		}
	}
	return index, false, nil
}

func persistedIndexPath(cacheDir string, graphHash [sha256.Size]byte) string {
	return filepath.Join(cacheDir, indexCachePrefix+hex.EncodeToString(graphHash[:])+indexCacheSuffix)
}

func loadPersistedIndex(path string, graphHash [sha256.Size]byte) (*Index, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxPersistedIndexBytes {
		return nil, false
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()

	decoder := gob.NewDecoder(io.LimitReader(file, maxPersistedIndexBytes+1))
	var cached persistedIndex
	if err := decoder.Decode(&cached); err != nil {
		return nil, false
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false
	}
	if !validPersistedIndex(cached, graphHash) {
		return nil, false
	}

	documents := make([]indexedNode, len(cached.Documents))
	for i, document := range cached.Documents {
		documents[i] = indexedNode{
			nameText:      document.NameText,
			pathText:      document.PathText,
			packageText:   document.PackageText,
			idText:        document.IDText,
			metaText:      document.MetaText,
			nameTerms:     document.NameTerms,
			nameTermOrder: document.NameTermOrder,
			pathTerms:     document.PathTerms,
			packageTerms:  document.PackageTerms,
			idTerms:       document.IDTerms,
			metaTerms:     document.MetaTerms,
			lengths:       fieldLengthsFromPersisted(document.Lengths),
		}
	}
	index := &Index{
		graph:             cached.Graph,
		docs:              documents,
		documentFrequency: cached.DocumentFrequency,
		trigramPostings:   cached.TrigramPostings,
		averageLength:     fieldLengthsFromPersisted(cached.AverageLength),
		idfCache:          map[string]float64{},
	}
	index.initializeGraphState()
	return index, true
}

func validPersistedIndex(cached persistedIndex, graphHash [sha256.Size]byte) bool {
	if cached.Format != indexCacheFormat || cached.GraphHash != graphHash || len(cached.Documents) != len(cached.Graph.Nodes) {
		return false
	}
	if !validFieldLengths(cached.AverageLength, len(cached.Documents) == 0) {
		return false
	}
	for _, document := range cached.Documents {
		if !validFieldLengths(document.Lengths, false) ||
			!validTermCounts(document.NameTerms) || !validTermCounts(document.PathTerms) ||
			!validTermCounts(document.PackageTerms) || !validTermCounts(document.IDTerms) ||
			!validTermCounts(document.MetaTerms) {
			return false
		}
	}
	for term, frequency := range cached.DocumentFrequency {
		if term == "" || frequency <= 0 || frequency > len(cached.Documents) {
			return false
		}
	}
	for trigram, postings := range cached.TrigramPostings {
		if trigram == "" || len(postings) == 0 {
			return false
		}
		previous := -1
		for _, documentIndex := range postings {
			if documentIndex <= previous || documentIndex < 0 || documentIndex >= len(cached.Documents) {
				return false
			}
			previous = documentIndex
		}
	}
	return true
}

func validTermCounts(terms map[string]int) bool {
	for term, count := range terms {
		if strings.TrimSpace(term) == "" || count <= 0 {
			return false
		}
	}
	return true
}

func validFieldLengths(lengths persistedFieldLengths, allowZero bool) bool {
	values := []float64{lengths.Name, lengths.Path, lengths.PackageName, lengths.ID, lengths.Meta}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || (!allowZero && value == 0) {
			return false
		}
	}
	return true
}

func writePersistedIndex(path string, graphHash [sha256.Size]byte, index *Index) (err error) {
	if index == nil {
		return fmt.Errorf("persist query index: nil index")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".query-index-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if temporaryPath != "" {
			_ = os.Remove(temporaryPath)
		}
	}()

	cached := persistedIndex{
		Format:            indexCacheFormat,
		GraphHash:         graphHash,
		Graph:             index.graph,
		Documents:         make([]persistedDocument, len(index.docs)),
		DocumentFrequency: index.documentFrequency,
		TrigramPostings:   index.trigramPostings,
		AverageLength:     persistedFieldLengthsFrom(index.averageLength),
	}
	for i, document := range index.docs {
		cached.Documents[i] = persistedDocument{
			NameText:      document.nameText,
			PathText:      document.pathText,
			PackageText:   document.packageText,
			IDText:        document.idText,
			MetaText:      document.metaText,
			NameTerms:     document.nameTerms,
			NameTermOrder: document.nameTermOrder,
			PathTerms:     document.pathTerms,
			PackageTerms:  document.packageTerms,
			IDTerms:       document.idTerms,
			MetaTerms:     document.metaTerms,
			Lengths:       persistedFieldLengthsFrom(document.lengths),
		}
	}
	if err := gob.NewEncoder(temporary).Encode(cached); err != nil {
		return err
	}
	info, err := temporary.Stat()
	if err != nil {
		return err
	}
	if info.Size() <= 0 || info.Size() > maxPersistedIndexBytes {
		return fmt.Errorf("persist query index: cache size %d exceeds limit", info.Size())
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	temporaryPath = ""
	return nil
}

func persistedFieldLengthsFrom(lengths fieldLengths) persistedFieldLengths {
	return persistedFieldLengths{
		Name: lengths.name, Path: lengths.path, PackageName: lengths.packageName, ID: lengths.id, Meta: lengths.meta,
	}
}

func fieldLengthsFromPersisted(lengths persistedFieldLengths) fieldLengths {
	return fieldLengths{
		name: lengths.Name, path: lengths.Path, packageName: lengths.PackageName, id: lengths.ID, meta: lengths.Meta,
	}
}

func prunePersistedIndexes(cacheDir, keep string) {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == keep || !strings.HasPrefix(name, indexCachePrefix) || !strings.HasSuffix(name, indexCacheSuffix) {
			continue
		}
		_ = os.Remove(filepath.Join(cacheDir, name))
	}
}

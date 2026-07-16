package treeanalyzer

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/12vault/ravel/internal/graph"
	"github.com/12vault/ravel/internal/lang"
	"github.com/12vault/ravel/internal/scan"
	"github.com/odvcencio/gotreesitter"
)

// InternalWorkerCommand is intentionally omitted from the public CLI help.
// The parent Ravel process uses it to isolate Tree-sitter runtimes and their
// process-wide parser state.
const InternalWorkerCommand = "__tree-analyzer-worker"

const (
	processWorkerProtocolVersion = 1
	// Match Graphify's cutoff: below this, process startup costs more than the
	// small amount of parsing it can parallelize.
	minProcessWorkerFiles = 20
)

var errProcessWorkerUnavailable = errors.New("tree analyzer process worker unavailable")

type processWorkerHello struct {
	Version int
}

type processWorkerRequest struct {
	Index         int
	Language      string
	File          scan.File
	TimeoutMicros uint64
}

type processWorkerResponse struct {
	Index       int
	Parsed      processParsedFile
	Diagnostics []graph.Diagnostic
	Error       string
	Contributed bool
}

type processParsedFile struct {
	File        scan.File
	Language    string
	Source      []byte
	Definitions []processDefinition
	References  []processReference
	Heritage    []gotreesitter.HeritageRef
	Imports     []gotreesitter.ImportRef
}

type processDefinition struct {
	ID        string
	Name      string
	Qualified string
	Kind      graph.NodeKind
	Path      string
	Language  string
	StartByte uint32
	EndByte   uint32
	StartLine int
	EndLine   int
	Column    int
}

type processReference struct {
	Name      string
	Kind      graph.EdgeKind
	Path      string
	Language  string
	StartByte uint32
	EndByte   uint32
	StartLine int
	Column    int
}

type processWorker struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	encoder *gob.Encoder
	decoder *gob.Decoder
	stderr  lockedBuffer
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

var newProcessWorkerCommand = defaultProcessWorkerCommand

func defaultProcessWorkerCommand(ctx context.Context) (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("%w: resolve executable: %v", errProcessWorkerUnavailable, err)
	}
	base := strings.ToLower(executable)
	if (strings.HasSuffix(base, ".test") || strings.HasSuffix(base, ".test.exe")) && os.Getenv("RAVEL_TREE_WORKER_TEST_BINARY") != "1" {
		return nil, fmt.Errorf("%w: test binary does not expose worker mode", errProcessWorkerUnavailable)
	}
	command := exec.CommandContext(ctx, executable, InternalWorkerCommand)
	// Each child parses one file at a time. Restricting it to one Go scheduler
	// thread prevents N workers from each behaving like an N-core process.
	command.Env = append(os.Environ(), "GOMAXPROCS=1")
	return command, nil
}

func isProcessWorkerUnavailable(err error) bool {
	return errors.Is(err, errProcessWorkerUnavailable)
}

func startProcessWorker(ctx context.Context) (*processWorker, error) {
	command, err := newProcessWorkerCommand(ctx)
	if err != nil {
		return nil, err
	}
	worker := &processWorker{command: command}
	command.Stderr = &worker.stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: create stdin: %v", errProcessWorkerUnavailable, err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("%w: create stdout: %v", errProcessWorkerUnavailable, err)
	}
	worker.stdin = stdin
	worker.encoder = gob.NewEncoder(stdin)
	worker.decoder = gob.NewDecoder(stdout)
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("%w: start: %v", errProcessWorkerUnavailable, err)
	}
	var hello processWorkerHello
	if err := worker.decoder.Decode(&hello); err != nil {
		_ = worker.stop()
		message := strings.TrimSpace(worker.stderr.String())
		if message != "" {
			return nil, fmt.Errorf("%w: handshake: %v: %s", errProcessWorkerUnavailable, err, message)
		}
		return nil, fmt.Errorf("%w: handshake: %v", errProcessWorkerUnavailable, err)
	}
	if hello.Version != processWorkerProtocolVersion {
		_ = worker.stop()
		return nil, fmt.Errorf("%w: protocol version %d", errProcessWorkerUnavailable, hello.Version)
	}
	return worker, nil
}

func (worker *processWorker) stop() error {
	if worker.stdin != nil {
		_ = worker.stdin.Close()
	}
	if worker.command == nil || worker.command.Process == nil {
		return nil
	}
	err := worker.command.Wait()
	if err == nil {
		return nil
	}
	if message := strings.TrimSpace(worker.stderr.String()); message != "" {
		return fmt.Errorf("%w: %s", err, message)
	}
	return err
}

func (worker *processWorker) parse(request processWorkerRequest) (processWorkerResponse, error) {
	if err := worker.encoder.Encode(request); err != nil {
		return processWorkerResponse{}, fmt.Errorf("send parse request: %w", err)
	}
	var response processWorkerResponse
	if err := worker.decoder.Decode(&response); err != nil {
		message := strings.TrimSpace(worker.stderr.String())
		if message != "" {
			return processWorkerResponse{}, fmt.Errorf("receive parse response: %w: %s", err, message)
		}
		return processWorkerResponse{}, fmt.Errorf("receive parse response: %w", err)
	}
	if response.Index != request.Index {
		return processWorkerResponse{}, fmt.Errorf("receive parse response: index %d, want %d", response.Index, request.Index)
	}
	return response, nil
}

func (a *Analyzer) analyzeWithProcessWorkers(ctx context.Context, files []scan.File, progress func(path string, completed int), workers int) (*lang.AnalysisResult, error) {
	result := &lang.AnalysisResult{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return result, nil
	}
	if workers < 1 {
		workers = 1
	}
	if workers > len(files) {
		workers = len(files)
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	pool := make([]*processWorker, 0, workers)
	for range workers {
		worker, err := startProcessWorker(workerCtx)
		if err != nil {
			cancel()
			for _, started := range pool {
				_ = started.stop()
			}
			return nil, err
		}
		pool = append(pool, worker)
	}

	outcomes := make([]parseOutcome, len(files))
	finished := make(chan int, workers)
	workerErrors := make(chan error, workers)
	var next atomic.Int64
	var wg sync.WaitGroup
	wg.Add(len(pool))
	for _, worker := range pool {
		go func(worker *processWorker) {
			defer wg.Done()
			defer func() {
				if err := worker.stop(); err != nil && workerCtx.Err() == nil {
					select {
					case workerErrors <- fmt.Errorf("stop tree analyzer worker: %w", err):
					default:
					}
				}
			}()
			for {
				if workerCtx.Err() != nil {
					return
				}
				index := int(next.Add(1)) - 1
				if index >= len(files) {
					return
				}
				response, err := worker.parse(processWorkerRequest{
					Index: index, Language: a.language, File: files[index],
					TimeoutMicros: uint64(parseTimeoutMicros) * uint64(workers),
				})
				if err != nil {
					select {
					case workerErrors <- fmt.Errorf("parse %s in worker: %w", files[index].Path, err):
					case <-workerCtx.Done():
					}
					cancel()
					return
				}
				outcomes[index] = parseOutcome{
					parsed: processParsedToParsedFile(response.Parsed), diagnostics: response.Diagnostics,
					contributed: response.Contributed,
				}
				if response.Error != "" {
					outcomes[index].err = errors.New(response.Error)
				}
				select {
				case finished <- index:
				case <-workerCtx.Done():
					return
				}
			}
		}(worker)
	}

	parsed := make([]parsedFile, 0, len(files))
	ready := make([]bool, len(files))
	for i, file := range files {
		if progress != nil {
			progress(file.Path, i)
		}
		for !ready[i] {
			select {
			case index := <-finished:
				ready[index] = true
			case err := <-workerErrors:
				cancel()
				wg.Wait()
				return nil, err
			case <-workerCtx.Done():
				wg.Wait()
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				select {
				case err := <-workerErrors:
					return nil, err
				default:
					return nil, workerCtx.Err()
				}
			}
		}
		outcome := outcomes[i]
		if outcome.err != nil {
			cancel()
			wg.Wait()
			return nil, outcome.err
		}
		result.Diagnostics = append(result.Diagnostics, outcome.diagnostics...)
		if outcome.contributed {
			parsed = append(parsed, outcome.parsed)
		}
	}
	wg.Wait()
	select {
	case err := <-workerErrors:
		return nil, err
	default:
	}
	if progress != nil {
		progress(files[len(files)-1].Path, len(files))
	}
	emitDefinitions(parsed, result)
	emitReferences(parsed, result)
	emitHeritage(parsed, result)
	emitImports(parsed, result)
	return result, nil
}

// RunProcessWorker serves the hidden single-threaded parser process protocol.
// It is exported only so the CLI package can route the hidden command without
// creating a package cycle.
func RunProcessWorker(ctx context.Context, input io.Reader, output io.Writer) error {
	encoder := gob.NewEncoder(output)
	decoder := gob.NewDecoder(input)
	if err := encoder.Encode(processWorkerHello{Version: processWorkerProtocolVersion}); err != nil {
		return fmt.Errorf("write tree analyzer worker handshake: %w", err)
	}
	for {
		var request processWorkerRequest
		if err := decoder.Decode(&request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read tree analyzer worker request: %w", err)
		}
		entry := entryForFile(request.Language, request.File.Path)
		response := processWorkerResponse{Index: request.Index}
		if entry == nil || entry.Language == nil {
			if err := encoder.Encode(response); err != nil {
				return fmt.Errorf("write tree analyzer worker response: %w", err)
			}
			continue
		}
		parsed, diagnostics, err := parseFile(ctx, request.File, *entry, request.TimeoutMicros)
		response.Parsed = parsedFileToProcessParsed(parsed)
		response.Diagnostics = diagnostics
		response.Contributed = true
		if err != nil {
			response.Error = err.Error()
		}
		gotreesitter.DrainArenaPools()
		if err := encoder.Encode(response); err != nil {
			return fmt.Errorf("write tree analyzer worker response: %w", err)
		}
	}
}

func parsedFileToProcessParsed(file parsedFile) processParsedFile {
	wire := processParsedFile{
		File: file.file, Language: file.language, Source: file.source,
		Heritage: file.heritage, Imports: file.imports,
		Definitions: make([]processDefinition, len(file.definitions)),
		References:  make([]processReference, len(file.references)),
	}
	for i, item := range file.definitions {
		wire.Definitions[i] = processDefinition{
			ID: item.id, Name: item.name, Qualified: item.qualified, Kind: item.kind,
			Path: item.path, Language: item.language, StartByte: item.startByte,
			EndByte: item.endByte, StartLine: item.startLine, EndLine: item.endLine, Column: item.column,
		}
	}
	for i, item := range file.references {
		wire.References[i] = processReference{
			Name: item.name, Kind: item.kind, Path: item.path, Language: item.language,
			StartByte: item.startByte, EndByte: item.endByte, StartLine: item.startLine, Column: item.column,
		}
	}
	return wire
}

func processParsedToParsedFile(wire processParsedFile) parsedFile {
	file := parsedFile{
		file: wire.File, language: wire.Language, source: wire.Source,
		heritage: wire.Heritage, imports: wire.Imports,
		definitions: make([]definition, len(wire.Definitions)),
		references:  make([]reference, len(wire.References)),
	}
	for i, item := range wire.Definitions {
		file.definitions[i] = definition{
			id: item.ID, name: item.Name, qualified: item.Qualified, kind: item.Kind,
			path: item.Path, language: item.Language, startByte: item.StartByte,
			endByte: item.EndByte, startLine: item.StartLine, endLine: item.EndLine, column: item.Column,
		}
	}
	for i, item := range wire.References {
		file.references[i] = reference{
			name: item.Name, kind: item.Kind, path: item.Path, language: item.Language,
			startByte: item.StartByte, endByte: item.EndByte, startLine: item.StartLine, column: item.Column,
		}
	}
	return file
}

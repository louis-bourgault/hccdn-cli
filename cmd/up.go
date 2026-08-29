package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/louis-bourgault/hccdn-cli/cdn"
	"github.com/louis-bourgault/hccdn-cli/db"
	"github.com/louis-bourgault/hccdn-cli/img"
	"github.com/louis-bourgault/hccdn-cli/types"
	"github.com/spf13/cobra"
)

const transformVersion = "v1"

type variant struct {
	setting string
	key     string
}

type sourcePlan struct {
	path         string
	relativePath string
	hash         string
	variants     []variant
}

type uploadTask struct {
	fileIndex    int
	variantIndex int
	source       sourcePlan
	variant      variant
}

type taskResult struct {
	task   uploadTask
	upload *types.Upload
	err    error
}

type preparedPayload struct {
	filename string
	hash     string
	data     []byte
	path     string
}

func (p preparedPayload) open() (io.ReadCloser, error) {
	if p.data != nil {
		return io.NopCloser(bytes.NewReader(p.data)), nil
	}
	return os.Open(p.path)
}

var upCmd = &cobra.Command{
	Use:   "up <file-or-directory>",
	Short: "Upload a file or directory",
	Args:  cobra.ExactArgs(1),
	RunE:  runUp,
}

func runUp(cmd *cobra.Command, args []string) (runErr error) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	apiKey := os.Getenv("HCCDN_API_KEY")
	if apiKey == "" {
		return errors.New("HCCDN_API_KEY is not set")
	}
	location, err := filepath.Abs(filepath.Clean(args[0]))
	if err != nil {
		return err
	}
	info, err := os.Stat(location)
	if err != nil {
		return err
	}

	settings, err := parseVariants(cmd)
	if err != nil {
		return err
	}
	recursive, err := cmd.Flags().GetBool("recursive")
	if err != nil {
		return err
	}
	files, err := collectFiles(location, info.IsDir(), recursive)
	if err != nil {
		return err
	}
	if len(files) == 0 && verbose {
		fmt.Fprintln(cmd.ErrOrStderr(), "No files found; writing an empty manifest.")
	}

	database, err := db.GetDB()
	if err != nil {
		return err
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	sessionID, err := database.BeginSession(strings.Join(os.Args[1:], " "), wd)
	if err != nil {
		return err
	}
	sessionStatus := "failed"
	defer func() {
		if err := database.FinishSession(sessionID, sessionStatus); err != nil && runErr == nil {
			runErr = err
		}
	}()
	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return err
	}
	if output == "" {
		if info.IsDir() {
			output = filepath.Join(location, sessionID+".hccdn.json")
		} else {
			output = location + ".hccdn.json"
		}
	} else if output != "-" {
		output, err = filepath.Abs(filepath.Clean(output))
		if err != nil {
			return err
		}
	}
	if output != "-" {
		for _, source := range files {
			if output == source {
				return fmt.Errorf("manifest output cannot overwrite source file %s", source)
			}
		}
	}

	plans, err := buildPlans(database, files, location, info.IsDir(), settings)
	if err != nil {
		return err
	}
	tasks := makeTasks(plans)
	workers, err := cmd.Flags().GetInt("workers")
	if err != nil {
		return err
	}
	retries, err := cmd.Flags().GetInt("retries")
	if err != nil {
		return err
	}
	timeout, err := cmd.Flags().GetDuration("timeout")
	if err != nil {
		return err
	}
	if workers < 1 {
		return errors.New("workers must be at least 1")
	}
	if retries < 0 {
		return errors.New("retries cannot be negative")
	}
	if timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	client := cdn.NewClient(apiKey, os.Getenv("HCCDN_API_URL"), timeout, retries)
	results := executeUploads(ctx, database, client, sessionID, tasks, workers, cmd.ErrOrStderr())
	skipped := 0
	for _, plan := range plans {
		if isOptimisable(plan.path) {
			skipped += len(settings) - len(plan.variants)
		}
	}

	manifest := make([]types.File, len(plans))
	for i, plan := range plans {
		manifest[i] = types.File{Path: plan.path, RelativePath: plan.relativePath}
	}
	var failures []error
	uploaded, reused := 0, 0
	for _, result := range results {
		if result.err != nil {
			failures = append(failures, fmt.Errorf("%s (%s): %w", result.task.source.path, result.task.variant.setting, result.err))
			continue
		}
		manifest[result.task.fileIndex].Uploads = append(manifest[result.task.fileIndex].Uploads, result.upload)
		if result.upload.Reused {
			reused++
		} else {
			uploaded++
		}
		if verbose {
			state := "uploaded"
			if result.upload.Reused {
				state = "reused"
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "%s %s (%s): %s\n", state, result.task.source.relativePath,
				result.task.variant.setting, result.upload.URL)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d upload(s) failed (successful uploads were saved and will be reused next time): %w", len(failures), errors.Join(failures...))
	}
	for i := range manifest {
		sort.SliceStable(manifest[i].Uploads, func(a, b int) bool {
			return variantOrder(manifest[i].Uploads[a].Optimised, settings) < variantOrder(manifest[i].Uploads[b].Optimised, settings)
		})
	}

	jsonBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	jsonStdout, err := cmd.Flags().GetBool("json")
	if err != nil {
		return err
	}
	if output != "-" {
		if err := atomicWrite(output, append(jsonBytes, '\n')); err != nil {
			return err
		}
	}
	if jsonStdout || output == "-" {
		fmt.Fprintln(cmd.OutOrStdout(), string(jsonBytes))
	} else if !quiet {
		fmt.Fprintf(cmd.OutOrStdout(), "Session %s: %d uploaded, %d reused, %d skipped; manifest: %s\n", sessionID, uploaded, reused, skipped, output)
	}
	sessionStatus = "complete"
	return nil
}

func parseVariants(cmd *cobra.Command) ([]variant, error) {
	value, err := cmd.Flags().GetString("optimise")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(value) == "" {
		value = "none"
	}
	seen := make(map[string]bool)
	var variants []variant
	for _, raw := range strings.Split(value, ",") {
		setting := strings.ToLower(strings.TrimSpace(raw))
		if setting == "" {
			return nil, errors.New("optimisation settings cannot be empty")
		}
		if setting != "none" && setting != "full" {
			size, err := strconv.Atoi(setting)
			if err != nil || size <= 0 {
				return nil, fmt.Errorf("invalid optimisation %q: use none, full, or a positive pixel size", raw)
			}
		}
		if seen[setting] {
			continue
		}
		seen[setting] = true
		key := "original:" + transformVersion
		if setting == "full" {
			key = "webp:" + transformVersion + ":q85:full"
		} else if setting != "none" {
			key = "webp:" + transformVersion + ":q85:max=" + setting
		}
		variants = append(variants, variant{setting: setting, key: key})
	}
	return variants, nil
}

func collectFiles(location string, directory, recursive bool) ([]string, error) {
	if !directory {
		if strings.HasSuffix(strings.ToLower(location), ".hccdn.json") {
			return nil, errors.New("refusing to upload a generated .hccdn.json manifest")
		}
		return []string{location}, nil
	}
	var files []string
	if recursive {
		err := filepath.WalkDir(location, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && !strings.HasSuffix(strings.ToLower(entry.Name()), ".hccdn.json") {
				files = append(files, path)
			}
			return nil
		})
		return files, err
	}
	entries, err := os.ReadDir(location)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && !strings.HasSuffix(strings.ToLower(entry.Name()), ".hccdn.json") {
			files = append(files, filepath.Join(location, entry.Name()))
		}
	}
	return files, nil
}

func buildPlans(database *db.DB, files []string, root string, directory bool, variants []variant) ([]sourcePlan, error) {
	plans := make([]sourcePlan, 0, len(files))
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		hash, cached, err := database.CachedHash(path, info.Size(), info.ModTime().UnixNano())
		if err != nil {
			return nil, err
		}
		if !cached {
			hash, err = hashFile(path)
			if err != nil {
				return nil, err
			}
			if err := database.SaveCachedHash(path, info.Size(), info.ModTime().UnixNano(), hash); err != nil {
				return nil, err
			}
		}
		relative := filepath.Base(path)
		if directory {
			relative, err = filepath.Rel(root, path)
			if err != nil {
				return nil, err
			}
		}
		planVariants := variants
		if !isOptimisable(path) {
			planVariants = []variant{{setting: "none", key: "original:" + transformVersion}}
		} else {
			planVariants, err = filterOversizedVariants(path, variants)
			if err != nil {
				return nil, err
			}
		}
		plans = append(plans, sourcePlan{path: path, relativePath: filepath.ToSlash(relative), hash: hash, variants: planVariants})
	}
	return plans, nil
}

func filterOversizedVariants(path string, variants []variant) ([]variant, error) {
	needsDimensions := false
	for _, variant := range variants {
		if variant.setting != "none" && variant.setting != "full" {
			needsDimensions = true
			break
		}
	}
	if !needsDimensions {
		return variants, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	config, _, err := image.DecodeConfig(file)
	file.Close()
	if err != nil {
		return nil, fmt.Errorf("decode image %s: %w", path, err)
	}
	filtered := make([]variant, 0, len(variants))
	for _, variant := range variants {
		if variant.setting == "none" || variant.setting == "full" {
			filtered = append(filtered, variant)
			continue
		}
		size, err := strconv.Atoi(variant.setting)
		if err != nil {
			return nil, err
		}
		if size < config.Width || size < config.Height {
			filtered = append(filtered, variant)
		}
	}
	return filtered, nil
}

func makeTasks(plans []sourcePlan) []uploadTask {
	var tasks []uploadTask
	for fileIndex, plan := range plans {
		for variantIndex, variant := range plan.variants {
			tasks = append(tasks, uploadTask{fileIndex: fileIndex, variantIndex: variantIndex, source: plan, variant: variant})
		}
	}
	return tasks
}

func executeUploads(ctx context.Context, database *db.DB, client *cdn.Client, sessionID string, tasks []uploadTask, workers int, progressWriter io.Writer) []taskResult {
	if workers < 1 {
		workers = 1
	}
	if workers > len(tasks) && len(tasks) > 0 {
		workers = len(tasks)
	}
	jobs := make(chan uploadTask)
	results := make(chan taskResult, len(tasks))
	locks := &keyedLocks{locks: make(map[string]*sync.Mutex)}
	var done atomic.Int64
	showProgress := isTerminal(progressWriter) && !quiet
	var progressMu sync.Mutex
	worker := func() {
		for task := range jobs {
			upload, err := processUpload(ctx, database, client, sessionID, task, locks)
			results <- taskResult{task: task, upload: upload, err: err}
			count := done.Add(1)
			if showProgress {
				progressMu.Lock()
				fmt.Fprintf(progressWriter, "\rProcessed %d/%d variants", count, len(tasks))
				progressMu.Unlock()
			}
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); worker() }()
	}
	go func() {
		for _, task := range tasks {
			jobs <- task
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	collected := make([]taskResult, 0, len(tasks))
	for result := range results {
		collected = append(collected, result)
	}
	if showProgress && len(tasks) > 0 {
		fmt.Fprintln(progressWriter)
	}
	sort.Slice(collected, func(i, j int) bool {
		if collected[i].task.fileIndex == collected[j].task.fileIndex {
			return collected[i].task.variantIndex < collected[j].task.variantIndex
		}
		return collected[i].task.fileIndex < collected[j].task.fileIndex
	})
	return collected
}

type keyedLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (k *keyedLocks) lock(key string) func() {
	k.mu.Lock()
	lock := k.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		k.locks[key] = lock
	}
	k.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func processUpload(ctx context.Context, database *db.DB, client *cdn.Client, sessionID string, task uploadTask, locks *keyedLocks) (*types.Upload, error) {
	unlock := locks.lock(task.source.hash + "\x00" + task.variant.key)
	defer unlock()

	if existing, found, err := database.FindUpload(task.source.hash, task.variant.key); err != nil {
		return nil, err
	} else if found {
		existing.Reused = true
		existing.Optimised = task.variant.setting
		existing.FileLoc = task.source.path
		if err := database.RecordUpload(existing, sessionID, task.source.path, true); err != nil {
			return nil, err
		}
		return existing, nil
	}

	payload, err := preparePayload(task.source.path, task.source.hash, task.variant.setting)
	if err != nil {
		return nil, err
	}
	if legacy, found, err := database.FindLegacyUpload(task.source.path, payload.filename); err != nil {
		return nil, err
	} else if found {
		remoteHash, err := client.RemoteSHA256(ctx, legacy.URL)
		if err != nil {
			return nil, fmt.Errorf("verify legacy upload %s: %w", legacy.ID, err)
		}
		if remoteHash == payload.hash {
			if err := database.BackfillUpload(legacy.ID, task.source.hash, payload.hash, task.variant.key); err != nil {
				return nil, err
			}
			legacy.SourceSHA256 = task.source.hash
			legacy.PayloadSHA256 = payload.hash
			legacy.VariantKey = task.variant.key
			legacy.Optimised = task.variant.setting
			legacy.FileLoc = task.source.path
			legacy.Reused = true
			if err := database.RecordUpload(legacy, sessionID, task.source.path, true); err != nil {
				return nil, err
			}
			return legacy, nil
		}
	}

	upload, err := client.Upload(ctx, cdn.Payload{Filename: payload.filename, Open: payload.open})
	if err != nil {
		return nil, err
	}
	upload.SourceSHA256 = task.source.hash
	upload.PayloadSHA256 = payload.hash
	upload.VariantKey = task.variant.key
	upload.Optimised = task.variant.setting
	upload.FileLoc = task.source.path
	upload.Reused = false
	if err := database.RecordUpload(upload, sessionID, task.source.path, false); err != nil {
		return nil, fmt.Errorf("save upload %s: %w", upload.ID, err)
	}
	return upload, nil
}

func preparePayload(path, sourceHash, setting string) (preparedPayload, error) {
	base := filepath.Base(path)
	if setting == "none" {
		return preparedPayload{filename: base, hash: sourceHash, path: path}, nil
	}
	buffer, err := img.OptimiseImage(path, setting)
	if err != nil {
		return preparedPayload{}, err
	}
	data := append([]byte(nil), buffer.Bytes()...)
	sum := sha256.Sum256(data)
	filename := base + ".webp"
	if setting != "full" {
		filename = base + setting + ".webp"
	}
	return preparedPayload{filename: filename, hash: hex.EncodeToString(sum[:]), data: data}, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func isOptimisable(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png":
		return true
	default:
		return false
	}
}

func variantOrder(setting string, variants []variant) int {
	for i, variant := range variants {
		if setting == variant.setting {
			return i
		}
	}
	return len(variants)
}

func atomicWrite(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".hccdn-manifest-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o644); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err == nil {
		return nil
	} else if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return err
	}
	return os.Rename(tempName, path)
}

func isTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func init() {
	rootCmd.AddCommand(upCmd)
	upCmd.Flags().StringP("optimise", "o", "", "Variants: none, full, or comma-separated maximum dimensions (for example none,full,720)")
	upCmd.Flags().BoolP("recursive", "r", false, "Upload directories recursively")
	upCmd.Flags().String("output", "", "Manifest path (use - for stdout)")
	upCmd.Flags().Bool("json", false, "Also print the manifest JSON to stdout")
	upCmd.Flags().IntP("workers", "w", 4, "Maximum concurrent uploads")
	upCmd.Flags().Int("retries", 2, "Retries for transient CDN failures")
	upCmd.Flags().Duration("timeout", 2*time.Minute, "Timeout for each CDN request")
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type config struct {
	baseURL       string
	apiKey        string
	repoRoot      string
	database      string
	table         string
	token         string
	idColumn      string
	titleColumn   string
	completedCol  string
	seed          int64
	steps         int
	loop          bool
	provision     bool
	keepResources bool
	failOn4xx     bool
	requestTimout time.Duration

	// Intensive test settings
	concurrency   int // Number of concurrent workers
	batchSize     int // Max rows per batch operation
	maxRows       int // Max rows for large reads (should be <= server limit)
	stressMode     bool
	stressWorkers  int
	stressDuration time.Duration

	// Benchmark settings
	benchmarkMode     bool
	benchmarkDuration time.Duration
	benchmarkWarmup   time.Duration
}

type provisionedResources struct {
	workspaceDir string
	templateName string
	databaseName string
}

type todo struct {
	ID        int
	Title     string
	Completed bool
}

type client struct {
	httpClient *http.Client
	baseURL    string
	database   string
	table      string
	token      string
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

type op struct {
	name   string
	weight int
	run    func(*rand.Rand, *runState) error
}

type runState struct {
	cfg    config
	client *client
	model  map[int]todo
	mu     sync.RWMutex // Protects model for concurrent access
	step   int
	seed   int64
}

type stressStats struct {
	requests  atomic.Int64
	errors    atomic.Int64
	latencyNs atomic.Int64
}

func main() {
	cfg := loadConfig()

	if cfg.token == "" {
		cfg.token = cfg.apiKey
	}

	var resources *provisionedResources
	if cfg.provision {
		provisioned, err := provisionTestResources(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to provision test resources: %v\n", err)
			os.Exit(1)
		}
		resources = provisioned
		cfg.database = resources.databaseName
		fmt.Printf("Provisioned template=%s database=%s\n", resources.templateName, resources.databaseName)

		if !cfg.keepResources {
			defer cleanupProvisionedResources(cfg, resources)
		}
	} else if cfg.database == "" {
		fmt.Fprintln(os.Stderr, "Database is required when -provision=false (set -database or ATOMICBASE_DATABASE)")
		os.Exit(1)
	}

	// Run stress test if enabled
	if cfg.stressMode {
		if err := runStressTest(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Stress test failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Run max RPS benchmark if enabled
	if cfg.benchmarkMode {
		if err := runBenchmark(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Benchmark failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if cfg.loop {
		run := int64(0)
		for {
			runSeed := cfg.seed + run
			if err := runSimulation(cfg, runSeed); err != nil {
				fmt.Fprintf(os.Stderr, "\nSimulation failed (seed=%d, run=%d): %v\n", runSeed, run, err)
				printReplayHint(cfg, runSeed)
				os.Exit(1)
			}
			run++
		}
	}

	if err := runSimulation(cfg, cfg.seed); err != nil {
		fmt.Fprintf(os.Stderr, "Simulation failed: %v\n", err)
		printReplayHint(cfg, cfg.seed)
		os.Exit(1)
	}
}

func runStressTest(cfg config) error {
	fmt.Printf("Starting stress test: workers=%d duration=%s\n", cfg.stressWorkers, cfg.stressDuration)

	httpClient := &http.Client{Timeout: cfg.requestTimout}
	c := &client{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(cfg.baseURL, "/"),
		database:   cfg.database,
		table:      cfg.table,
		token:      cfg.token,
	}

	// Verify connection
	s := &runState{cfg: cfg, client: c, model: map[int]todo{}}
	if err := s.healthcheck(); err != nil {
		return err
	}

	var stats stressStats
	ctx, cancel := context.WithTimeout(context.Background(), cfg.stressDuration)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < cfg.stressWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))
			runStressWorker(ctx, c, cfg, r, &stats)
		}(i)
	}

	// Progress reporting
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reqs := stats.requests.Load()
				errs := stats.errors.Load()
				totalLatency := stats.latencyNs.Load()
				avgLatency := time.Duration(0)
				if reqs > 0 {
					avgLatency = time.Duration(totalLatency / reqs)
				}
				fmt.Printf("\rRequests: %d | Errors: %d | Avg Latency: %v     ", reqs, errs, avgLatency)
			}
		}
	}()

	wg.Wait()
	fmt.Println()

	reqs := stats.requests.Load()
	errs := stats.errors.Load()
	totalLatency := stats.latencyNs.Load()
	avgLatency := time.Duration(0)
	if reqs > 0 {
		avgLatency = time.Duration(totalLatency / reqs)
	}

	fmt.Printf("Stress test complete:\n")
	fmt.Printf("  Total requests: %d\n", reqs)
	fmt.Printf("  Errors: %d (%.2f%%)\n", errs, float64(errs)/float64(reqs)*100)
	fmt.Printf("  Avg latency: %v\n", avgLatency)
	fmt.Printf("  Throughput: %.2f req/s\n", float64(reqs)/cfg.stressDuration.Seconds())

	return nil
}

type benchmarkStats struct {
	requests   atomic.Int64
	errors     atomic.Int64
	latencies  []int64 // nanoseconds, collected for percentile calculation
	latencyMu  sync.Mutex
	startTime  time.Time
	windowReqs []int64 // requests per second for each window
	windowMu   sync.Mutex
	errorMsgs  map[string]int64 // deduplicated error messages with counts
	errorMu    sync.Mutex
}

func runBenchmark(cfg config) error {
	fmt.Printf("=== Max RPS Benchmark ===\n")
	fmt.Printf("Warmup: %s | Duration: %s\n", cfg.benchmarkWarmup, cfg.benchmarkDuration)

	httpClient := &http.Client{
		Timeout: cfg.requestTimout,
		Transport: &http.Transport{
			MaxIdleConns:        1000,
			MaxIdleConnsPerHost: 1000,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	c := &client{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(cfg.baseURL, "/"),
		database:   cfg.database,
		table:      cfg.table,
		token:      cfg.token,
	}

	// Verify connection
	s := &runState{cfg: cfg, client: c, model: map[int]todo{}}
	if err := s.healthcheck(); err != nil {
		return err
	}

	// Seed some data first
	fmt.Print("Seeding test data...")
	for i := 0; i < 100; i++ {
		t := todo{ID: i + 1, Title: fmt.Sprintf("bench-%d", i), Completed: false}
		body := map[string]any{"data": []map[string]any{todoToMap(cfg, t)}}
		c.query("insert,on-conflict=replace", body)
	}
	fmt.Println(" done")

	// Determine worker count - start with many workers to saturate
	numWorkers := 100
	if cfg.stressWorkers > 0 {
		numWorkers = cfg.stressWorkers
	}

	var stats benchmarkStats
	stats.startTime = time.Now()
	stats.errorMsgs = make(map[string]int64)

	totalDuration := cfg.benchmarkWarmup + cfg.benchmarkDuration
	ctx, cancel := context.WithTimeout(context.Background(), totalDuration)
	defer cancel()

	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))
			runBenchmarkWorker(ctx, c, r, &stats, cfg.benchmarkWarmup)
		}(i)
	}

	// Progress reporter
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		lastReqs := int64(0)
		secondNum := 0
		warmupSecs := int(cfg.benchmarkWarmup.Seconds())

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				secondNum++
				reqs := stats.requests.Load()
				rps := reqs - lastReqs
				lastReqs = reqs
				errs := stats.errors.Load()

				// Only record RPS after warmup
				if secondNum > warmupSecs {
					stats.windowMu.Lock()
					stats.windowReqs = append(stats.windowReqs, rps)
					stats.windowMu.Unlock()
				}

				status := "WARMUP"
				if secondNum > warmupSecs {
					status = "BENCH"
				}
				fmt.Printf("\r[%s] RPS: %5d | Total: %6d | Errors: %d     ", status, rps, reqs, errs)
			}
		}
	}()

	wg.Wait()
	fmt.Println()

	// Calculate results
	stats.windowMu.Lock()
	windowReqs := stats.windowReqs
	stats.windowMu.Unlock()

	if len(windowReqs) == 0 {
		return fmt.Errorf("no benchmark data collected")
	}

	// Calculate stats
	var totalRPS int64
	var peakRPS int64
	for _, rps := range windowReqs {
		totalRPS += rps
		if rps > peakRPS {
			peakRPS = rps
		}
	}
	avgRPS := totalRPS / int64(len(windowReqs))

	// Get latency percentiles
	stats.latencyMu.Lock()
	latencies := stats.latencies
	stats.latencyMu.Unlock()

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	var p50, p90, p99 time.Duration
	if len(latencies) > 0 {
		p50 = time.Duration(latencies[len(latencies)*50/100])
		p90 = time.Duration(latencies[len(latencies)*90/100])
		p99 = time.Duration(latencies[len(latencies)*99/100])
	}

	fmt.Printf("\n=== Benchmark Results ===\n")
	fmt.Printf("Workers:     %d\n", numWorkers)
	fmt.Printf("Duration:    %s (after %s warmup)\n", cfg.benchmarkDuration, cfg.benchmarkWarmup)
	fmt.Printf("Total Reqs:  %d\n", stats.requests.Load())
	fmt.Printf("Errors:      %d (%.2f%%)\n", stats.errors.Load(), float64(stats.errors.Load())/float64(stats.requests.Load())*100)
	fmt.Printf("\n")
	fmt.Printf("Peak RPS:    %d\n", peakRPS)
	fmt.Printf("Avg RPS:     %d\n", avgRPS)
	fmt.Printf("\n")
	fmt.Printf("Latency P50: %v\n", p50)
	fmt.Printf("Latency P90: %v\n", p90)
	fmt.Printf("Latency P99: %v\n", p99)

	// Print deduplicated errors
	stats.errorMu.Lock()
	errorMsgs := stats.errorMsgs
	stats.errorMu.Unlock()

	if len(errorMsgs) > 0 {
		fmt.Printf("\n=== Errors ===\n")

		// Sort by count (descending)
		type errCount struct {
			msg   string
			count int64
		}
		sorted := make([]errCount, 0, len(errorMsgs))
		for msg, count := range errorMsgs {
			sorted = append(sorted, errCount{msg, count})
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].count > sorted[j].count
		})

		for _, e := range sorted {
			fmt.Printf("  %5d × %s\n", e.count, e.msg)
		}
	}

	return nil
}

func runBenchmarkWorker(ctx context.Context, c *client, r *rand.Rand, stats *benchmarkStats, warmup time.Duration) {
	warmupEnd := stats.startTime.Add(warmup)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		start := time.Now()
		isWarmup := start.Before(warmupEnd)

		// Simple select query - fastest operation to measure max throughput
		body := map[string]any{
			"select": []any{"*"},
			"limit":  10,
		}
		status, _, apiErr, err := c.query("select", body)

		elapsed := time.Since(start)

		// Only count after warmup
		if !isWarmup {
			stats.requests.Add(1)
			if err != nil || status >= 400 {
				stats.errors.Add(1)

				// Record error message
				var errMsg string
				if err != nil {
					errMsg = err.Error()
				} else if apiErr != nil {
					errMsg = fmt.Sprintf("[%d] %s: %s", status, apiErr.Code, apiErr.Message)
				} else {
					errMsg = fmt.Sprintf("[%d] unknown error", status)
				}

				stats.errorMu.Lock()
				stats.errorMsgs[errMsg]++
				stats.errorMu.Unlock()
			}

			// Sample latencies (collect ~10% to avoid memory issues)
			if r.Intn(10) == 0 {
				stats.latencyMu.Lock()
				stats.latencies = append(stats.latencies, int64(elapsed))
				stats.latencyMu.Unlock()
			}
		}
	}
}

func runStressWorker(ctx context.Context, c *client, cfg config, r *rand.Rand, stats *stressStats) {
	ops := []func(*rand.Rand, *client, config) error{
		stressInsert,
		stressSelect,
		stressUpdate,
		stressDelete,
		stressBatchInsert,
		stressLargeSelect,
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		start := time.Now()
		op := ops[r.Intn(len(ops))]
		err := op(r, c, cfg)
		elapsed := time.Since(start)

		stats.requests.Add(1)
		stats.latencyNs.Add(int64(elapsed))
		if err != nil {
			stats.errors.Add(1)
		}
	}
}

func stressInsert(r *rand.Rand, c *client, cfg config) error {
	t := randomTodo(r)
	body := map[string]any{
		"data": []map[string]any{todoToMap(cfg, t)},
	}
	status, _, _, err := c.query("insert,on-conflict=replace", body)
	if err != nil {
		return err
	}
	if status >= 500 {
		return fmt.Errorf("insert failed status=%d", status)
	}
	return nil
}

func stressSelect(r *rand.Rand, c *client, cfg config) error {
	body := map[string]any{
		"select": []any{"*"},
		"limit":  r.Intn(100) + 1,
	}
	status, _, _, err := c.query("select", body)
	if err != nil {
		return err
	}
	if status >= 500 {
		return fmt.Errorf("select failed status=%d", status)
	}
	return nil
}

func stressUpdate(r *rand.Rand, c *client, cfg config) error {
	id := r.Intn(500) + 1
	body := map[string]any{
		"data": map[string]any{
			cfg.titleColumn:  fmt.Sprintf("stress-%d", r.Intn(1_000_000)),
			cfg.completedCol: r.Intn(2) == 0,
		},
		"where": []map[string]any{{
			cfg.idColumn: map[string]any{"eq": id},
		}},
	}
	status, _, _, err := c.query("update", body)
	if err != nil {
		return err
	}
	if status >= 500 {
		return fmt.Errorf("update failed status=%d", status)
	}
	return nil
}

func stressDelete(r *rand.Rand, c *client, cfg config) error {
	id := r.Intn(500) + 1
	body := map[string]any{
		"where": []map[string]any{{
			cfg.idColumn: map[string]any{"eq": id},
		}},
	}
	status, _, _, err := c.query("delete", body)
	if err != nil {
		return err
	}
	if status >= 500 {
		return fmt.Errorf("delete failed status=%d", status)
	}
	return nil
}

func stressBatchInsert(r *rand.Rand, c *client, cfg config) error {
	batchSize := r.Intn(cfg.batchSize) + 1
	data := make([]map[string]any, batchSize)
	for i := 0; i < batchSize; i++ {
		t := randomTodo(r)
		data[i] = todoToMap(cfg, t)
	}
	body := map[string]any{"data": data}
	status, _, _, err := c.query("insert,on-conflict=replace", body)
	if err != nil {
		return err
	}
	if status >= 500 {
		return fmt.Errorf("batch insert failed status=%d", status)
	}
	return nil
}

func stressLargeSelect(r *rand.Rand, c *client, cfg config) error {
	limit := r.Intn(cfg.maxRows-100) + 100 // 100 to maxRows
	body := map[string]any{
		"select": []any{"*"},
		"limit":  limit,
	}
	status, _, _, err := c.query("select", body)
	if err != nil {
		return err
	}
	if status >= 500 {
		return fmt.Errorf("large select failed status=%d", status)
	}
	return nil
}

func runSimulation(cfg config, seed int64) error {
	r := rand.New(rand.NewSource(seed))
	httpClient := &http.Client{Timeout: cfg.requestTimout}
	c := &client{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(cfg.baseURL, "/"),
		database:   cfg.database,
		table:      cfg.table,
		token:      cfg.token,
	}

	s := &runState{
		cfg:    cfg,
		client: c,
		model:  map[int]todo{},
		seed:   seed,
	}

	if err := s.healthcheck(); err != nil {
		return err
	}

	if err := s.warmupAndValidateTable(); err != nil {
		return err
	}

	ops := []op{
		{name: "insert", weight: 20, run: runInsert},
		{name: "update", weight: 15, run: runUpdate},
		{name: "upsert", weight: 15, run: runUpsert},
		{name: "delete", weight: 10, run: runDelete},
		{name: "select", weight: 10, run: runSelect},
		{name: "batch_insert", weight: 10, run: runBatchInsert},
		{name: "batch_upsert", weight: 5, run: runBatchUpsert},
		{name: "batch_delete", weight: 5, run: runBatchDelete},
		{name: "large_select", weight: 5, run: runLargeSelect},
		{name: "paginated_select", weight: 5, run: runPaginatedSelect},
	}

	// Add concurrent ops if concurrency > 1
	if cfg.concurrency > 1 {
		ops = append(ops, op{name: "concurrent_mixed", weight: 10, run: runConcurrentMixed})
	}

	fmt.Printf("Starting deterministic simulation: steps=%d seed=%d table=%s database=%s concurrency=%d\n",
		cfg.steps, seed, cfg.table, cfg.database, cfg.concurrency)

	for i := 1; i <= cfg.steps; i++ {
		s.step = i

		chosen := weightedOp(r, ops)
		if err := chosen.run(r, s); err != nil {
			return fmt.Errorf("step %d (%s): %w", i, chosen.name, err)
		}

		// Verify less frequently for performance (every 10 steps)
		if i%10 == 0 || i == cfg.steps {
			if err := s.verifySnapshot(); err != nil {
				return fmt.Errorf("step %d (%s): %w", i, chosen.name, err)
			}
		}

		if i%100 == 0 {
			fmt.Printf("\rProgress: %d/%d steps (%.1f%%)   ", i, cfg.steps, float64(i)/float64(cfg.steps)*100)
		}
	}

	fmt.Printf("\rSimulation passed: steps=%d seed=%d                    \n", cfg.steps, seed)
	return nil
}

func loadConfig() config {
	defaultSeed := time.Now().Unix()
	wd, _ := os.Getwd()
	repoRootDefault := envOr("SIM_REPO_ROOT", filepath.Clean(filepath.Join(wd, "..")))

	baseURL := envOr("ATOMICBASE_BASE_URL", "http://localhost:8080")
	apiKey := envOr("ATOMICBASE_API_KEY", "")
	database := envOr("ATOMICBASE_DATABASE", "")
	table := envOr("ATOMICBASE_TABLE", "todos")
	token := envOr("ATOMICBASE_TOKEN", "")
	idCol := envOr("ATOMICBASE_ID_COLUMN", "id")
	titleCol := envOr("ATOMICBASE_TITLE_COLUMN", "title")
	completedCol := envOr("ATOMICBASE_COMPLETED_COLUMN", "completed")
	repoRoot := repoRootDefault

	steps := envIntOr("SIM_STEPS", 500)
	seed := envInt64Or("SIM_SEED", defaultSeed)
	loop := envBoolOr("SIM_LOOP", false)
	provision := envBoolOr("SIM_PROVISION", true)
	keepResources := envBoolOr("SIM_KEEP_RESOURCES", false)
	failOn4xx := envBoolOr("SIM_FAIL_ON_4XX", false)
	timeoutMS := envIntOr("SIM_TIMEOUT_MS", 5000)

	// Intensive test settings
	concurrency := envIntOr("SIM_CONCURRENCY", 1)
	batchSize := envIntOr("SIM_BATCH_SIZE", 50)
	maxRows := envIntOr("SIM_MAX_ROWS", 500)
	stressMode := envBoolOr("SIM_STRESS", false)
	stressWorkers := envIntOr("SIM_STRESS_WORKERS", 10)
	stressDurationSec := envIntOr("SIM_STRESS_DURATION", 30)

	// Benchmark settings
	benchmarkMode := envBoolOr("SIM_BENCHMARK", false)
	benchmarkDurationSec := envIntOr("SIM_BENCHMARK_DURATION", 10)
	benchmarkWarmupSec := envIntOr("SIM_BENCHMARK_WARMUP", 2)

	flag.StringVar(&baseURL, "base-url", baseURL, "Atomicbase API base URL")
	flag.StringVar(&apiKey, "api-key", apiKey, "API key for CLI provisioning and API auth")
	flag.StringVar(&repoRoot, "repo-root", repoRoot, "Atomicbase repo root (for invoking CLI)")
	flag.StringVar(&database, "database", database, "Database header value")
	flag.StringVar(&table, "table", table, "Table to test")
	flag.StringVar(&token, "token", token, "Bearer token (optional if API auth disabled)")
	flag.StringVar(&idCol, "id-column", idCol, "ID column")
	flag.StringVar(&titleCol, "title-column", titleCol, "Title/value column")
	flag.StringVar(&completedCol, "completed-column", completedCol, "Completed/boolean column")
	flag.IntVar(&steps, "steps", steps, "Operations per run")
	flag.Int64Var(&seed, "seed", seed, "Deterministic seed")
	flag.BoolVar(&loop, "loop", loop, "Run forever, incrementing seed per run")
	flag.BoolVar(&provision, "provision", provision, "Provision template/database via CLI before simulation")
	flag.BoolVar(&keepResources, "keep-resources", keepResources, "Keep provisioned template/database after run")
	flag.BoolVar(&failOn4xx, "fail-on-4xx", failOn4xx, "Fail when API returns any 4xx")
	flag.IntVar(&timeoutMS, "timeout-ms", timeoutMS, "HTTP timeout in milliseconds")

	// Intensive test flags
	flag.IntVar(&concurrency, "concurrency", concurrency, "Number of concurrent operations")
	flag.IntVar(&batchSize, "batch-size", batchSize, "Max rows per batch operation")
	flag.IntVar(&maxRows, "max-rows", maxRows, "Max rows for large reads (should be <= server limit)")
	flag.BoolVar(&stressMode, "stress", stressMode, "Run stress test mode")
	flag.IntVar(&stressWorkers, "stress-workers", stressWorkers, "Number of stress test workers")
	flag.IntVar(&stressDurationSec, "stress-duration", stressDurationSec, "Stress test duration in seconds")
	flag.BoolVar(&benchmarkMode, "benchmark", benchmarkMode, "Run max RPS benchmark")
	flag.IntVar(&benchmarkDurationSec, "benchmark-duration", benchmarkDurationSec, "Benchmark duration in seconds")
	flag.IntVar(&benchmarkWarmupSec, "benchmark-warmup", benchmarkWarmupSec, "Benchmark warmup period in seconds")
	flag.Parse()

	return config{
		baseURL:        baseURL,
		apiKey:         apiKey,
		repoRoot:       repoRoot,
		database:       database,
		table:          table,
		token:          token,
		idColumn:       idCol,
		titleColumn:    titleCol,
		completedCol:   completedCol,
		steps:          steps,
		seed:           seed,
		loop:           loop,
		provision:      provision,
		keepResources:  keepResources,
		failOn4xx:      failOn4xx,
		requestTimout:  time.Duration(timeoutMS) * time.Millisecond,
		concurrency:    concurrency,
		batchSize:      batchSize,
		maxRows:        maxRows,
		stressMode:        stressMode,
		stressWorkers:     stressWorkers,
		stressDuration:    time.Duration(stressDurationSec) * time.Second,
		benchmarkMode:     benchmarkMode,
		benchmarkDuration: time.Duration(benchmarkDurationSec) * time.Second,
		benchmarkWarmup:   time.Duration(benchmarkWarmupSec) * time.Second,
	}
}

func (s *runState) healthcheck() error {
	req, err := http.NewRequest(http.MethodGet, s.client.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	res, err := s.client.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("healthcheck status=%d body=%s", res.StatusCode, string(body))
	}
	return nil
}

func (s *runState) warmupAndValidateTable() error {
	body := map[string]any{
		"select": []any{"*"},
		"limit":  1,
	}
	status, raw, _, err := s.client.query("select", body)
	if err != nil {
		return err
	}
	if status >= 400 {
		return fmt.Errorf("table warmup failed status=%d response=%s", status, string(raw))
	}
	return nil
}

func runInsert(r *rand.Rand, s *runState) error {
	t := randomTodo(r)
	body := map[string]any{
		"data": []map[string]any{todoToMap(s.cfg, t)},
	}

	status, raw, apiErr, err := s.client.query("insert", body)
	if err != nil {
		return err
	}

	if status >= 400 {
		if shouldFailStatus(s.cfg, status) {
			return fmt.Errorf("insert rejected status=%d error=%+v raw=%s", status, apiErr, string(raw))
		}
		return nil
	}

	s.model[t.ID] = t
	return nil
}

func runBatchInsert(r *rand.Rand, s *runState) error {
	batchSize := r.Intn(s.cfg.batchSize) + 1
	data := make([]map[string]any, batchSize)
	todos := make([]todo, batchSize)

	for i := 0; i < batchSize; i++ {
		t := randomTodo(r)
		todos[i] = t
		data[i] = todoToMap(s.cfg, t)
	}

	body := map[string]any{"data": data}
	status, raw, apiErr, err := s.client.query("insert", body)
	if err != nil {
		return err
	}

	if status >= 400 {
		if shouldFailStatus(s.cfg, status) {
			return fmt.Errorf("batch insert rejected status=%d error=%+v raw=%s", status, apiErr, string(raw))
		}
		return nil
	}

	for _, t := range todos {
		s.model[t.ID] = t
	}
	return nil
}

func runBatchUpsert(r *rand.Rand, s *runState) error {
	batchSize := r.Intn(s.cfg.batchSize) + 1
	data := make([]map[string]any, batchSize)
	todos := make([]todo, batchSize)

	for i := 0; i < batchSize; i++ {
		t := randomTodo(r)
		todos[i] = t
		data[i] = todoToMap(s.cfg, t)
	}

	body := map[string]any{"data": data}
	status, raw, apiErr, err := s.client.query("insert,on-conflict=replace", body)
	if err != nil {
		return err
	}

	if status >= 400 {
		if shouldFailStatus(s.cfg, status) {
			return fmt.Errorf("batch upsert rejected status=%d error=%+v raw=%s", status, apiErr, string(raw))
		}
		return nil
	}

	for _, t := range todos {
		s.model[t.ID] = t
	}
	return nil
}

func runBatchDelete(r *rand.Rand, s *runState) error {
	// Delete multiple IDs using IN clause
	numToDelete := r.Intn(10) + 1
	ids := make([]int, numToDelete)
	for i := 0; i < numToDelete; i++ {
		ids[i] = pickKnownOrRandomID(r, s.model)
	}

	body := map[string]any{
		"where": []map[string]any{{
			s.cfg.idColumn: map[string]any{"in": ids},
		}},
	}

	status, raw, apiErr, err := s.client.query("delete", body)
	if err != nil {
		return err
	}

	if status >= 400 {
		if shouldFailStatus(s.cfg, status) {
			return fmt.Errorf("batch delete rejected status=%d error=%+v raw=%s", status, apiErr, string(raw))
		}
		return nil
	}

	for _, id := range ids {
		delete(s.model, id)
	}
	return nil
}

func runUpsert(r *rand.Rand, s *runState) error {
	t := randomTodo(r)
	body := map[string]any{
		"data": []map[string]any{todoToMap(s.cfg, t)},
	}

	status, raw, apiErr, err := s.client.query("insert,on-conflict=replace", body)
	if err != nil {
		return err
	}

	if status >= 400 {
		if shouldFailStatus(s.cfg, status) {
			return fmt.Errorf("upsert rejected status=%d error=%+v raw=%s", status, apiErr, string(raw))
		}
		return nil
	}

	s.model[t.ID] = t
	return nil
}

func runUpdate(r *rand.Rand, s *runState) error {
	id := pickKnownOrRandomID(r, s.model)
	newCompleted := r.Intn(2) == 0
	newTitle := fmt.Sprintf("sim-upd-%d", r.Intn(1_000_000))
	body := map[string]any{
		"data": map[string]any{
			s.cfg.titleColumn:  newTitle,
			s.cfg.completedCol: newCompleted,
		},
		"where": []map[string]any{{
			s.cfg.idColumn: map[string]any{"eq": id},
		}},
	}

	status, raw, apiErr, err := s.client.query("update", body)
	if err != nil {
		return err
	}

	if status >= 400 {
		if shouldFailStatus(s.cfg, status) {
			return fmt.Errorf("update rejected status=%d error=%+v raw=%s", status, apiErr, string(raw))
		}
		return nil
	}

	if curr, ok := s.model[id]; ok {
		curr.Title = newTitle
		curr.Completed = newCompleted
		s.model[id] = curr
	}
	return nil
}

func runDelete(r *rand.Rand, s *runState) error {
	id := pickKnownOrRandomID(r, s.model)
	body := map[string]any{
		"where": []map[string]any{{
			s.cfg.idColumn: map[string]any{"eq": id},
		}},
	}

	status, raw, apiErr, err := s.client.query("delete", body)
	if err != nil {
		return err
	}

	if status >= 400 {
		if shouldFailStatus(s.cfg, status) {
			return fmt.Errorf("delete rejected status=%d error=%+v raw=%s", status, apiErr, string(raw))
		}
		return nil
	}

	delete(s.model, id)
	return nil
}

func runSelect(r *rand.Rand, s *runState) error {
	body := map[string]any{"select": []any{"*"}}
	if r.Intn(3) == 0 {
		id := pickKnownOrRandomID(r, s.model)
		body["where"] = []map[string]any{{
			s.cfg.idColumn: map[string]any{"eq": id},
		}}
	}

	status, raw, apiErr, err := s.client.query("select", body)
	if err != nil {
		return err
	}

	if status >= 400 {
		if shouldFailStatus(s.cfg, status) {
			return fmt.Errorf("select rejected status=%d error=%+v raw=%s", status, apiErr, string(raw))
		}
		return nil
	}

	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return fmt.Errorf("select decode failed: %w raw=%s", err, string(raw))
	}
	return nil
}

func runLargeSelect(r *rand.Rand, s *runState) error {
	// Select up to maxRows
	limit := r.Intn(s.cfg.maxRows-10) + 10
	body := map[string]any{
		"select": []any{"*"},
		"limit":  limit,
		"order":  []any{s.cfg.idColumn},
	}

	status, raw, apiErr, err := s.client.query("select", body)
	if err != nil {
		return err
	}

	if status >= 400 {
		if shouldFailStatus(s.cfg, status) {
			return fmt.Errorf("large select rejected status=%d error=%+v raw=%s", status, apiErr, string(raw))
		}
		return nil
	}

	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return fmt.Errorf("large select decode failed: %w raw=%s", err, string(raw))
	}
	return nil
}

func runPaginatedSelect(r *rand.Rand, s *runState) error {
	// Test offset/limit pagination
	pageSize := r.Intn(50) + 10
	offset := r.Intn(100)

	body := map[string]any{
		"select": []any{"*"},
		"limit":  pageSize,
		"offset": offset,
		"order":  []any{s.cfg.idColumn},
	}

	status, raw, apiErr, err := s.client.query("select", body)
	if err != nil {
		return err
	}

	if status >= 400 {
		if shouldFailStatus(s.cfg, status) {
			return fmt.Errorf("paginated select rejected status=%d error=%+v raw=%s", status, apiErr, string(raw))
		}
		return nil
	}

	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return fmt.Errorf("paginated select decode failed: %w raw=%s", err, string(raw))
	}
	return nil
}

func runConcurrentMixed(r *rand.Rand, s *runState) error {
	// Run multiple operations concurrently
	numOps := r.Intn(s.cfg.concurrency) + 2

	var wg sync.WaitGroup
	errCh := make(chan error, numOps)

	for i := 0; i < numOps; i++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			localR := rand.New(rand.NewSource(seed))

			ops := []func(*rand.Rand, *runState) error{
				runSelect,
				runLargeSelect,
				runPaginatedSelect,
			}

			// Only include write ops sometimes to avoid too many conflicts
			if localR.Intn(3) == 0 {
				ops = append(ops, runInsert, runUpsert)
			}

			op := ops[localR.Intn(len(ops))]
			if err := op(localR, s); err != nil {
				errCh <- err
			}
		}(r.Int63())
	}

	wg.Wait()
	close(errCh)

	// Return first error if any
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *runState) verifySnapshot() error {
	body := map[string]any{"select": []any{"*"}}
	status, raw, apiErr, err := s.client.query("select", body)
	if err != nil {
		return err
	}
	if status >= 400 {
		return fmt.Errorf("snapshot select failed status=%d error=%+v raw=%s", status, apiErr, string(raw))
	}

	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return fmt.Errorf("snapshot decode failed: %w raw=%s", err, string(raw))
	}

	actual := map[int]todo{}
	for _, row := range rows {
		id, ok := toInt(row[s.cfg.idColumn])
		if !ok {
			continue
		}
		title, _ := row[s.cfg.titleColumn].(string)
		completed, _ := toBool(row[s.cfg.completedCol])
		actual[id] = todo{ID: id, Title: title, Completed: completed}
	}

	if !equalTodoMaps(s.model, actual) {
		return fmt.Errorf("model mismatch\nexpected=%s\nactual=%s", formatTodos(s.model), formatTodos(actual))
	}

	return nil
}

func provisionTestResources(cfg config) (*provisionedResources, error) {
	if cfg.repoRoot == "" {
		return nil, fmt.Errorf("repo root is required for provisioning")
	}

	fullTestDir := filepath.Join(cfg.repoRoot, "full-test")
	if err := os.MkdirAll(fullTestDir, 0o755); err != nil {
		return nil, err
	}

	workspaceDir, err := os.MkdirTemp(fullTestDir, "sim-work-")
	if err != nil {
		return nil, err
	}

	nameSuffix := fmt.Sprintf("%d-%d", cfg.seed, time.Now().Unix())
	templateName := "full-test-template-" + sanitizeName(nameSuffix)
	databaseName := "full-test-db-" + sanitizeName(nameSuffix)

	if err := os.MkdirAll(filepath.Join(workspaceDir, "schemas"), 0o755); err != nil {
		return nil, err
	}

	configFile := fmt.Sprintf(`export default {
  url: %q,
  apiKey: %q,
  schemas: "./schemas",
};
`, cfg.baseURL, cfg.apiKey)

	if err := os.WriteFile(filepath.Join(workspaceDir, "atomicbase.config.ts"), []byte(configFile), 0o644); err != nil {
		return nil, err
	}

	templateImportPath := filepath.ToSlash(filepath.Join(cfg.repoRoot, "packages", "template", "dist", "index.js"))
	schemaPath := filepath.Join(workspaceDir, "schemas", templateName+".schema.ts")
	if err := os.WriteFile(schemaPath, []byte(complexSchemaTS(templateName, templateImportPath, cfg.seed)), 0o644); err != nil {
		return nil, err
	}

	if err := runAtomicbaseCLI(cfg.repoRoot, workspaceDir, "templates", "push", templateName); err != nil {
		return nil, fmt.Errorf("templates push failed: %w", err)
	}

	if err := runAtomicbaseCLI(cfg.repoRoot, workspaceDir, "databases", "create", databaseName, "--template", templateName); err != nil {
		_ = runAtomicbaseCLI(cfg.repoRoot, workspaceDir, "templates", "delete", templateName, "--force")
		return nil, fmt.Errorf("databases create failed: %w", err)
	}

	return &provisionedResources{
		workspaceDir: workspaceDir,
		templateName: templateName,
		databaseName: databaseName,
	}, nil
}

func cleanupProvisionedResources(cfg config, resources *provisionedResources) {
	if resources == nil {
		return
	}

	if err := runAtomicbaseCLI(cfg.repoRoot, resources.workspaceDir, "databases", "delete", resources.databaseName, "--force"); err != nil {
		fmt.Fprintf(os.Stderr, "Cleanup warning (database delete): %v\n", err)
	}

	if err := runAtomicbaseCLI(cfg.repoRoot, resources.workspaceDir, "templates", "delete", resources.templateName, "--force"); err != nil {
		fmt.Fprintf(os.Stderr, "Cleanup warning (template delete): %v\n", err)
	}

	if err := os.RemoveAll(resources.workspaceDir); err != nil {
		fmt.Fprintf(os.Stderr, "Cleanup warning (workspace remove): %v\n", err)
	}
}

func runAtomicbaseCLI(repoRoot, workspaceDir string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Primary path: run the CLI package directly through pnpm in the workspace.
	cliArgs := append([]string{"--filter", "@atomicbase/cli", "exec", "node", "bin/atomicbase.js"}, args...)
	cmd := exec.CommandContext(ctx, "pnpm", cliArgs...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "INIT_CWD="+workspaceDir)

	out, err := cmd.CombinedOutput()
	if err == nil {
		if len(out) > 0 {
			fmt.Print(string(out))
		}
		return nil
	}

	// Fallback path: invoke the CLI entry file by absolute path.
	fallbackArgs := append([]string{filepath.Join(repoRoot, "packages", "cli", "bin", "atomicbase.js")}, args...)
	fallback := exec.CommandContext(ctx, "node", fallbackArgs...)
	fallback.Dir = workspaceDir
	fallback.Env = append(os.Environ(), "INIT_CWD="+workspaceDir)

	fallbackOut, fallbackErr := fallback.CombinedOutput()
	if fallbackErr != nil {
		return fmt.Errorf("pnpm invocation failed:\n%s\nnode fallback failed: %w\n%s", string(out), fallbackErr, string(fallbackOut))
	}

	if len(fallbackOut) > 0 {
		fmt.Print(string(fallbackOut))
	}

	return nil
}

func complexSchemaTS(templateName, templateImportPath string, seed int64) string {
	r := rand.New(rand.NewSource(seed))

	minDisplayLen := 2 + r.Intn(4)
	minProjectNameLen := 3 + r.Intn(4)
	minTodoTitleLen := 3 + r.Intn(5)
	maxPriority := 5 + r.Intn(5)

	emailCollation := "NOCASE"
	if r.Intn(3) == 0 {
		emailCollation = "RTRIM"
	}

	todoStatuses := []string{"todo", "in_progress", "done"}
	if r.Intn(2) == 0 {
		todoStatuses = append(todoStatuses, "blocked")
	}
	if r.Intn(2) == 0 {
		todoStatuses = append(todoStatuses, "review")
	}

	statusList := make([]string, 0, len(todoStatuses))
	for _, s := range todoStatuses {
		statusList = append(statusList, fmt.Sprintf("'%s'", s))
	}

	todoFTSColumns := "[\"title\", \"description\"]"
	if r.Intn(2) == 0 {
		todoFTSColumns = "[\"title\", \"description\", \"metadata_json\"]"
	}

	optionalTodoCols := ""
	if r.Intn(2) == 0 {
		optionalTodoCols += "\n    archived_at: c.text(),"
	}
	if r.Intn(2) == 0 {
		optionalTodoCols += "\n    deleted_at: c.text(),"
	}
	if r.Intn(2) == 0 {
		optionalTodoCols += "\n    sprint_order: c.integer().notNull().default(0).check(\"sprint_order >= 0\"),"
	}

	extraTables := ""
	if r.Intn(2) == 0 {
		extraTables += `
  project_members: defineTable({
    project_id: c.integer().primaryKey().references("projects.id", { onDelete: "CASCADE", onUpdate: "CASCADE" }),
    user_id: c.integer().primaryKey().references("users.id", { onDelete: "CASCADE", onUpdate: "CASCADE" }),
    permission: c.text().notNull().default("editor").check("permission in ('viewer','editor','admin')"),
    joined_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
  }).index("idx_project_members_user", ["user_id"]),

`
	}
	if r.Intn(2) == 0 {
		extraTables += `
  todo_reactions: defineTable({
    todo_id: c.integer().primaryKey().references("todos.id", { onDelete: "CASCADE", onUpdate: "CASCADE" }),
    user_id: c.integer().primaryKey().references("users.id", { onDelete: "CASCADE", onUpdate: "CASCADE" }),
    emoji: c.text().primaryKey(),
    created_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
  }),

`
	}

	return fmt.Sprintf(`import { defineSchema, defineTable, c, sql } from %q;

export default defineSchema(%q, {
  users: defineTable({
    id: c.integer().primaryKey(),
    email: c.text().notNull().unique().collate(%q),
    display_name: c.text().notNull().check("length(display_name) >= %d"),
    role: c.text().notNull().default("member").check("role in ('owner','admin','member')"),
    profile_json: c.text().notNull().default("{}"),
    created_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
    updated_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
  }).index("idx_users_role", ["role"]),

  workspaces: defineTable({
    id: c.integer().primaryKey(),
    slug: c.text().notNull().unique().collate("NOCASE"),
    name: c.text().notNull(),
    owner_id: c.integer().notNull().references("users.id", { onDelete: "CASCADE", onUpdate: "CASCADE" }),
    created_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
  }).index("idx_workspaces_owner", ["owner_id"]),

  projects: defineTable({
    id: c.integer().primaryKey(),
    workspace_id: c.integer().notNull().references("workspaces.id", { onDelete: "CASCADE", onUpdate: "CASCADE" }),
    owner_id: c.integer().notNull().references("users.id", { onDelete: "RESTRICT", onUpdate: "CASCADE" }),
    name: c.text().notNull().check("length(name) >= %d"),
    description: c.text(),
    status: c.text().notNull().default("active").check("status in ('active','archived')"),
    priority: c.integer().notNull().default(3).check("priority between 1 and %d"),
    budget: c.real().check("budget >= 0"),
    slug: c.text().generatedAs("lower(replace(name, ' ', '-'))", { stored: true }),
    created_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
  })
    .index("idx_projects_workspace", ["workspace_id"])
    .index("idx_projects_status", ["status"])
    .uniqueIndex("idx_projects_workspace_slug", ["workspace_id", "slug"]),

  tags: defineTable({
    id: c.integer().primaryKey(),
    workspace_id: c.integer().notNull().references("workspaces.id", { onDelete: "CASCADE", onUpdate: "CASCADE" }),
    name: c.text().notNull().collate("NOCASE"),
    color: c.text().default("#cccccc").check("length(color) = 7"),
  }).uniqueIndex("idx_tags_workspace_name", ["workspace_id", "name"]),

  project_tags: defineTable({
    project_id: c.integer().primaryKey().references("projects.id", { onDelete: "CASCADE", onUpdate: "CASCADE" }),
    tag_id: c.integer().primaryKey().references("tags.id", { onDelete: "CASCADE", onUpdate: "CASCADE" }),
    added_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
  }),

  todos: defineTable({
    id: c.integer().primaryKey(),
    project_id: c.integer().notNull().references("projects.id", { onDelete: "CASCADE", onUpdate: "CASCADE" }),
    assignee_id: c.integer().references("users.id", { onDelete: "SET NULL", onUpdate: "CASCADE" }),
    title: c.text().notNull().check("length(title) >= %d"),
    description: c.text(),
    status: c.text().notNull().default("todo").check("status in (%s)"),
    priority: c.integer().notNull().default(3).check("priority between 1 and %d"),
    completed: c.integer().notNull().default(0).check("completed in (0,1)"),
    due_at: c.text(),
    estimate_hours: c.real().check("estimate_hours >= 0"),
    metadata_json: c.text().notNull().default("{}"),
    search_text: c.text().generatedAs("coalesce(title,'') || ' ' || coalesce(description,'')", { stored: true }),%s
    created_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
    updated_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
  })
    .index("idx_todos_project", ["project_id"])
    .index("idx_todos_assignee", ["assignee_id"])
    .index("idx_todos_status_priority", ["status", "priority"])
    .fts(%s),

  comments: defineTable({
    id: c.integer().primaryKey(),
    todo_id: c.integer().notNull().references("todos.id", { onDelete: "CASCADE", onUpdate: "CASCADE" }),
    author_id: c.integer().notNull().references("users.id", { onDelete: "RESTRICT", onUpdate: "CASCADE" }),
    body: c.text().notNull().check("length(body) > 0"),
    metadata_json: c.text().notNull().default("{}"),
    created_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
  })
    .index("idx_comments_todo", ["todo_id"])
    .fts(["body"]),

  attachments: defineTable({
    id: c.integer().primaryKey(),
    todo_id: c.integer().notNull().references("todos.id", { onDelete: "CASCADE", onUpdate: "CASCADE" }),
    filename: c.text().notNull(),
    content_type: c.text().notNull(),
    size_bytes: c.integer().notNull().check("size_bytes >= 0"),
    checksum: c.text().notNull(),
    content: c.blob(),
    created_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
  }).index("idx_attachments_todo", ["todo_id"]),

%s  audit_events: defineTable({
    todo_id: c.integer().primaryKey().references("todos.id", { onDelete: "CASCADE", onUpdate: "CASCADE" }),
    seq: c.integer().primaryKey(),
    actor_id: c.integer().references("users.id", { onDelete: "SET NULL", onUpdate: "CASCADE" }),
    action: c.text().notNull(),
    payload_json: c.text().notNull().default("{}"),
    created_at: c.text().notNull().default(sql("CURRENT_TIMESTAMP")),
  }).index("idx_audit_actor", ["actor_id"]),
});
`, templateImportPath, templateName, emailCollation, minDisplayLen, minProjectNameLen, maxPriority, minTodoTitleLen, strings.Join(statusList, ","), maxPriority, optionalTodoCols, todoFTSColumns, extraTables)
}

func sanitizeName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('-')
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "sim"
	}
	return out
}

func (c *client) query(preferOperation string, body map[string]any) (int, []byte, *apiError, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, nil, nil, err
	}

	url := fmt.Sprintf("%s/data/query/%s", c.baseURL, c.table)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Database", c.database)
	req.Header.Set("Prefer", "operation="+preferOperation)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer service."+c.token)
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return res.StatusCode, nil, nil, err
	}

	var e apiError
	if res.StatusCode >= 400 {
		_ = json.Unmarshal(raw, &e)
		return res.StatusCode, raw, &e, nil
	}

	return res.StatusCode, raw, nil, nil
}

func shouldFailStatus(cfg config, status int) bool {
	if status >= 500 {
		return true
	}
	if status >= 400 && cfg.failOn4xx {
		return true
	}
	return false
}

func weightedOp(r *rand.Rand, ops []op) op {
	totalWeight := 0
	for _, o := range ops {
		totalWeight += o.weight
	}

	v := r.Intn(totalWeight)
	cumulative := 0
	for _, o := range ops {
		cumulative += o.weight
		if v < cumulative {
			return o
		}
	}
	return ops[len(ops)-1]
}

func randomTodo(r *rand.Rand) todo {
	return todo{
		ID:        r.Intn(500) + 1,
		Title:     fmt.Sprintf("sim-%d", r.Intn(1_000_000)),
		Completed: r.Intn(2) == 0,
	}
}

func pickKnownOrRandomID(r *rand.Rand, m map[int]todo) int {
	if len(m) > 0 && r.Intn(100) < 75 {
		i := r.Intn(len(m))
		j := 0
		for id := range m {
			if j == i {
				return id
			}
			j++
		}
	}
	return r.Intn(500) + 1
}

func todoToMap(cfg config, t todo) map[string]any {
	return map[string]any{
		cfg.idColumn:     t.ID,
		cfg.titleColumn:  t.Title,
		cfg.completedCol: t.Completed,
	}
}

func equalTodoMaps(a, b map[int]todo) bool {
	if len(a) != len(b) {
		return false
	}
	for id, x := range a {
		y, ok := b[id]
		if !ok {
			return false
		}
		if x.Title != y.Title || x.Completed != y.Completed {
			return false
		}
	}
	return true
}

func formatTodos(m map[int]todo) string {
	ids := make([]int, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		t := m[id]
		out = append(out, fmt.Sprintf("%d:{title=%q completed=%t}", id, t.Title, t.Completed))
	}
	return "[" + strings.Join(out, ", ") + "]"
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case int64:
		return int(x), true
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

func toBool(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case float64:
		return x != 0, true
	case int:
		return x != 0, true
	default:
		return false, false
	}
}

func envOr(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func envIntOr(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envInt64Or(key string, fallback int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func envBoolOr(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func printReplayHint(cfg config, seed int64) {
	fmt.Println("Replay:")
	fmt.Printf("go run . -base-url %q -database %q -table %q -seed %d -steps %d\n",
		cfg.baseURL, cfg.database, cfg.table, seed, cfg.steps)
}

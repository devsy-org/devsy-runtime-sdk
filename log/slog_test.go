package log_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	pluginlog "github.com/devsy-org/devsy-runtime-sdk/log"
)

const (
	infoMessage   = "info message"
	warnMessage   = "warn message"
	errorMessage  = "error message"
	fatalMessage  = "fatal message"
	debugMessage  = "debug message"
	traceLevel    = "trace"
	warnLevel     = "warn"
	errorLevel    = "error"
	invalidFormat = "secret-format"
	traceMessage  = "trace message"
	nestedMessage = "nested message"
	providerKey   = "provider"
	messageKey    = "msg"
	userComponent = "user_component"
	userProvider  = "user_provider"
)

type logCase struct {
	name     string
	level    string
	format   string
	present  []string
	absent   []string
	warnings map[string]int
	text     bool
}

type resolvedValue struct{}

const (
	firstFormatWarning  = "first:invalid DEVSY_LOG_FORMAT; using json"
	secondFormatWarning = "second:invalid DEVSY_LOG_FORMAT; using json"
	providerComponent   = "component=provider"
)

func (resolvedValue) LogValue() slog.Value {
	return slog.GroupValue(slog.Int("count", 7), slog.Bool("ready", true))
}

type inlineCollisionValue struct{}

func (inlineCollisionValue) LogValue() slog.Value {
	return slog.GroupValue(slog.String("component", "valuer component"))
}

func TestPluginLoggerProcess(t *testing.T) {
	if os.Getenv("DEVSY_TEST_LOG_CHILD") == "1" {
		checkLoggerStdout(t, func() { runLogChild(t) })
		return
	}

	for _, tc := range append(levelCases(), formatCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			checkLogCase(t, tc)
		})
	}
}

func levelCases() []logCase {
	return []logCase{
		{
			name: "defaults",
			present: []string{
				infoMessage,
				warnMessage,
				errorMessage,
				fatalMessage,
				nestedMessage,
				"first remains",
			},
			absent: []string{traceMessage, debugMessage, "second filtered"},
		},
		{
			name:    traceLevel,
			level:   traceLevel,
			present: []string{traceMessage, debugMessage, infoMessage, fatalMessage},
		},
		{
			name:    "debug",
			level:   "debug",
			present: []string{debugMessage, infoMessage},
			absent:  []string{traceMessage},
		},
		{
			name:    warnLevel,
			level:   warnLevel,
			present: []string{warnMessage, errorMessage},
			absent:  []string{traceMessage, debugMessage, infoMessage},
		},
		{
			name:    errorLevel,
			level:   errorLevel,
			present: []string{errorMessage, fatalMessage},
			absent:  []string{warnMessage, infoMessage},
		},
	}
}

func formatCases() []logCase {
	return []logCase{
		{
			name:    "invalid",
			level:   "secret-level",
			format:  invalidFormat,
			present: []string{infoMessage},
			absent:  []string{"secret-level", invalidFormat, debugMessage},
			warnings: map[string]int{
				"first:invalid DEVSY_LOG_LEVEL; using info": 1,
				firstFormatWarning:                          1,
				secondFormatWarning:                         1,
			},
		},
		{
			name:    "invalid format above warning threshold",
			level:   errorLevel,
			format:  invalidFormat,
			present: []string{errorMessage},
			absent:  []string{invalidFormat, infoMessage},
			warnings: map[string]int{
				firstFormatWarning:  1,
				secondFormatWarning: 1,
			},
		},
		{
			name:    "unknown format at error level",
			level:   errorLevel,
			format:  "unknown-format",
			present: []string{errorMessage},
			absent:  []string{infoMessage, "unknown-format"},
			warnings: map[string]int{
				firstFormatWarning:  1,
				secondFormatWarning: 1,
			},
		},
		{name: "case folded", level: "DEBUG", format: "AUTO", present: []string{debugMessage}},
		{name: "auto", format: "auto", present: []string{infoMessage}},
		{name: "json", format: "json", present: []string{infoMessage}},
		{
			name:    "text",
			format:  "text",
			present: []string{infoMessage, providerComponent, "provider=first"},
			text:    true,
		},
		{
			name:    "plain",
			format:  "plain",
			present: []string{infoMessage, providerComponent},
			text:    true,
		},
		{
			name:    "logfmt",
			format:  "LoGfMt",
			present: []string{infoMessage, providerComponent, "provider=first"},
			text:    true,
		},
	}
}

func runLogChild(t *testing.T) {
	t.Helper()
	logger := pluginlog.NewPluginLogger("first")
	logger.Log(context.Background(), slog.Level(-8), traceMessage)
	logger.Debug(debugMessage)
	logger.Info(infoMessage)
	logger.Warn(warnMessage)
	logger.Error(errorMessage)
	logger.Error("credential canary", "value", os.Getenv("DEVSY_TEST_API_TOKEN"))
	logger.Log(context.Background(), slog.Level(12), fatalMessage)
	logger.With("request", 42).
		WithGroup("operation").
		Info(nestedMessage, "result", resolvedValue{})
	if err := os.Setenv("DEVSY_LOG_LEVEL", errorLevel); err != nil {
		t.Fatal(err)
	}
	second := pluginlog.NewPluginLogger("second")
	second.Info("second filtered")
	second.Error("second error")
	logger.Info("first remains")
}

func checkLoggerStdout(t *testing.T, logCalls func()) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "logger-stdout-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	func() {
		original := os.Stdout
		os.Stdout = file
		defer func() { os.Stdout = original }()
		logCalls()
	}()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != 0 {
		t.Fatalf("logger wrote to stdout: %q", output)
	}
}

func checkLogCase(t *testing.T, tc logCase) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestPluginLoggerProcess$")
	cmd.Env = append(
		os.Environ(),
		"DEVSY_TEST_LOG_CHILD=1",
		"DEVSY_LOG_LEVEL="+tc.level,
		"DEVSY_LOG_FORMAT="+tc.format,
		"DEVSY_TEST_API_TOKEN=unique-private-environment-canary-67291",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("child: %v; stdout: %s; stderr: %s", err, stdout.String(), stderr.String())
	}
	output := stderr.String()
	if strings.Contains(output, "unique-private-environment-canary-67291") ||
		!strings.Contains(output, "***") {
		t.Fatalf("environment credential not redacted: %s", output)
	}
	checkMessages(t, output, tc)
	if !tc.text {
		checkJSONRecords(t, output, tc.warnings)
	}
}

func checkMessages(t *testing.T, output string, tc logCase) {
	t.Helper()
	for _, want := range tc.present {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q in %s", want, output)
		}
	}
	for _, unwanted := range tc.absent {
		if strings.Contains(output, unwanted) {
			t.Errorf("unexpected %q in %s", unwanted, output)
		}
	}
}

func checkJSONRecords(t *testing.T, output string, expected map[string]int) {
	t.Helper()
	warnings := make(map[string]int)
	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid JSON %q: %v", line, err)
		}
		checkEnvelope(t, record)
		collectWarning(t, record, warnings)
		checkRecord(t, record)
	}
	if !maps.Equal(warnings, expected) {
		t.Errorf("configuration warnings = %v, want %v", warnings, expected)
	}
}

func checkEnvelope(t *testing.T, record map[string]any) {
	t.Helper()
	ts, ok := record["ts"].(string)
	if !ok {
		t.Fatalf("missing timestamp: %v", record)
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000Z0700", ts); err != nil {
		t.Errorf("invalid timestamp %q: %v", ts, err)
	}
	if record["component"] != providerKey || record[providerKey] == nil ||
		record["level"] == nil || record[messageKey] == nil || record["time"] != nil {
		t.Errorf("invalid envelope: %v", record)
	}
}

func collectWarning(t *testing.T, record map[string]any, warnings map[string]int) {
	t.Helper()
	msg, ok := record[messageKey].(string)
	if !ok || !strings.HasPrefix(msg, "invalid DEVSY_LOG_") {
		return
	}
	if record["level"] != warnLevel {
		t.Errorf("configuration warning level: %v", record)
	}
	provider, ok := record[providerKey].(string)
	if !ok {
		t.Fatalf("invalid warning provider: %v", record)
	}
	warnings[provider+":"+msg]++
}

func checkRecord(t *testing.T, record map[string]any) {
	t.Helper()
	switch record[messageKey] {
	case traceMessage:
		checkRecordField(t, record, "level", traceLevel)
	case fatalMessage:
		checkRecordField(t, record, "level", "fatal")
	case nestedMessage:
		checkNestedRecord(t, record)
	case "second error":
		checkRecordField(t, record, providerKey, "second")
	case "first remains":
		checkRecordField(t, record, providerKey, "first")
	}
}

func checkRecordField(t *testing.T, record map[string]any, key, want string) {
	t.Helper()
	if record[key] != want {
		t.Errorf("%s = %v, want %q: %v", key, record[key], want, record)
	}
}

func checkNestedRecord(t *testing.T, record map[string]any) {
	t.Helper()
	operation, ok := record["operation"].(map[string]any)
	if !ok {
		t.Fatalf("missing group: %v", record)
	}
	result, ok := operation["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result: %v", record)
	}
	ready, ok := result["ready"].(bool)
	if !ok || !ready || result["count"] != float64(7) || record["request"] != float64(42) {
		t.Errorf("nested attrs: %v", record)
	}
}

func TestReservedAttributeCollisions(t *testing.T) {
	if os.Getenv("DEVSY_TEST_COLLISIONS") == "1" {
		checkLoggerStdout(t, emitCollisionRecords)
		return
	}
	output := runCollisionChild(t)
	want := []map[string]any{
		{
			messageKey:     "record collision",
			"user_ts":      float64(1),
			"user_time":    "caller time",
			"user_level":   "caller level",
			"user_msg":     "caller message",
			"user_message": true,
			userComponent:  "caller component",
			userProvider:   "caller provider",
		},
		{
			messageKey:    "with collision",
			userProvider:  "with provider",
			"user_ts":     "with time",
			"user_msg":    "inline message",
			"user_level":  float64(7),
			userComponent: "valuer component",
			"ordinary":    float64(9),
		},
		{
			messageKey: "group collision",
			userProvider: map[string]any{
				messageKey: nestedMessage,
				"level":    float64(2),
				"value":    true,
			},
		},
		{
			messageKey:    "ordinary group",
			userComponent: "with component",
			"operation":   map[string]any{providerKey: "nested provider", "time": "nested time"},
		},
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != len(want) {
		t.Fatalf("got %d records, want %d: %s", len(lines), len(want), output)
	}
	for i, line := range lines {
		fields := decodeUniqueFields(t, line)
		for _, key := range []string{"ts", "level", messageKey, "component", providerKey} {
			if _, ok := fields[key]; !ok {
				t.Errorf("record %d missing %s: %s", i, key, line)
			}
		}
		checkEnvelope(t, fields)
		checkRecordField(t, fields, "level", "info")
		checkRecordField(t, fields, "component", providerKey)
		checkRecordField(t, fields, providerKey, "original")
		for key, value := range want[i] {
			if !reflect.DeepEqual(fields[key], value) {
				t.Errorf("record %d %s = %v, want %v", i, key, fields[key], value)
			}
		}
	}
}

func emitCollisionRecords() {
	logger := pluginlog.NewPluginLogger("original")
	logger.Info(
		"record collision",
		"ts",
		1,
		"time",
		"caller time",
		"level",
		"caller level",
		messageKey,
		"caller message",
		"message",
		true,
		"component",
		"caller component",
		providerKey,
		"caller provider",
	)
	logger.With(providerKey, "with provider", slog.Group("", slog.String("ts", "with time"))).
		WithGroup("").Info("with collision",
		slog.Group("", slog.String(messageKey, "inline message"), slog.Group("", slog.Int("level", 7))),
		slog.Any("", inlineCollisionValue{}), "ordinary", 9)
	logger.WithGroup(providerKey).
		Info("group collision", messageKey, nestedMessage, "level", 2, "value", true)
	logger.With("component", "with component").WithGroup("operation").
		With(providerKey, "nested provider").Info("ordinary group", "time", "nested time")
}

func runCollisionChild(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestReservedAttributeCollisions$")
	cmd.Env = append(
		os.Environ(),
		"DEVSY_TEST_COLLISIONS=1",
		"DEVSY_LOG_LEVEL=info",
		"DEVSY_LOG_FORMAT=json",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("child: %v; stdout: %s; stderr: %s", err, stdout.String(), stderr.String())
	}
	return stderr.String()
}

func decodeUniqueFields(t *testing.T, line string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(line))
	open, err := decoder.Token()
	if err != nil || open != json.Delim('{') {
		t.Fatalf("invalid object %q: %v", line, err)
	}
	fields := make(map[string]any)
	for decoder.More() {
		key := uniqueFieldKey(t, decoder, fields, line)
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatalf("invalid value for %q: %v", key, err)
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		t.Fatalf("invalid object end: %v", err)
	}
	return fields
}

func uniqueFieldKey(
	t *testing.T,
	decoder *json.Decoder,
	fields map[string]any,
	line string,
) string {
	t.Helper()
	token, err := decoder.Token()
	if err != nil {
		t.Fatalf("invalid key: %v", err)
	}
	key, ok := token.(string)
	if !ok {
		t.Fatalf("invalid key: %v", token)
	}
	if _, exists := fields[key]; exists {
		t.Fatalf("duplicate key %q: %s", key, line)
	}
	return key
}

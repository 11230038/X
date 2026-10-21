// Command test_environment checks whether this machine has what X needs before
// it is started: the toolchains, the Go dependencies and frontend packages, the
// backend's dotenv file, and the local services the backend talks to.
//
// Run it from anywhere inside the repository:
//
//	go run test_environment.go
//
// It exits 0 when nothing is missing and 1 when at least one item is. It stays a
// single standard-library file because the repository root is not a Go module:
// `go run .` would not work there, `go run test_environment.go` does.
//
// It never prints a value from .env — only key names — because that file holds
// real credentials. Deep validation (durations, URLs, enums) is still the
// backend's job at startup; this checks breadth, not depth.
package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// Vite 8's engines field: ^20.19.0 || >=22.12.0.
	nodeMinMajor = 20
	nodeMinMinor = 19
)

type checker struct {
	root     string
	missing  int
	warnings int
}

func (c *checker) okf(format string, args ...any) {
	fmt.Printf("  [OK]   "+format+"\n", args...)
}

func (c *checker) missingf(format string, args ...any) {
	c.missing++
	fmt.Printf("  [缺失] "+format+"\n", args...)
}

func (c *checker) warnf(format string, args ...any) {
	c.warnings++
	fmt.Printf("  [警告] "+format+"\n", args...)
}

func (c *checker) infof(format string, args ...any) {
	fmt.Printf("  [信息] "+format+"\n", args...)
}

func main() {
	root, err := findRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	c := &checker{root: root}
	fmt.Printf("X 环境自检（%s）\n", root)
	fmt.Println(strings.Repeat("─", 60))

	fmt.Println("\n项目结构")
	c.checkLayout()

	fmt.Println("\n工具链")
	c.checkToolchains()

	fmt.Println("\nGo 依赖")
	c.checkGoDependencies()

	fmt.Println("\n前端依赖")
	c.checkFrontendDependencies()

	fmt.Println("\n配置（backend/.env）")
	values := c.checkDotenv()

	fmt.Println("\n数据库")
	c.checkDatabase(values)

	fmt.Println("\n本地服务")
	c.checkServices(values)

	if c.missing > 0 {
		fmt.Printf("\n结果：%d 项缺失，%d 项警告。补齐后再跑一遍。\n", c.missing, c.warnings)
		os.Exit(1)
	}
	if c.warnings > 0 {
		fmt.Printf("\n结果：环境已配齐，%d 项警告可以不影响启动。\n", c.warnings)
		return
	}
	fmt.Println("\n结果：环境已配齐。")
}

// findRoot walks up from the working directory until it finds the repository.
func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("读不到当前目录：%w", err)
	}
	for {
		backend := filepath.Join(dir, "backend", "go.mod")
		frontend := filepath.Join(dir, "frontend", "package.json")
		if isFile(backend) && isFile(frontend) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("找不到项目根目录（需要同时存在 backend/go.mod 与 frontend/package.json）；请在仓库内运行")
		}
		dir = parent
	}
}

func (c *checker) checkLayout() {
	files := []struct {
		path string
		hint string
	}{
		{"backend/main.go", "后端的入口文件不在了，仓库可能不完整"},
		{"backend/go.mod", "后端的模块文件不在了，仓库可能不完整"},
		{"backend/.env.example", "缺少配置模板，无法核对 .env 是否完整"},
		{"backend/internal/config/config.go", "缺少配置定义，无法核对必填键"},
		{"frontend/package.json", "前端的清单文件不在了，仓库可能不完整"},
	}
	for _, f := range files {
		if isFile(filepath.Join(c.root, f.path)) {
			c.okf("%s", f.path)
		} else {
			c.missingf("%s 不存在：%s", f.path, f.hint)
		}
	}
	if isDir(filepath.Join(c.root, "frontend", "node_modules")) {
		c.okf("frontend/node_modules 存在")
	} else {
		c.missingf("frontend/node_modules 不存在：先在 frontend/ 下执行 npm install")
	}
}

func (c *checker) checkToolchains() {
	if version, ok := c.commandVersion("Go", "go", "version"); ok {
		installed, required := parseVersion(version), requiredGoVersion(c.root)
		switch {
		case installed == nil || required == nil:
			c.okf("Go %s", version)
		case slices.Compare(installed, required) < 0:
			c.missingf("Go %s 低于 backend/go.mod 要求的 %d.%d：升级 Go 后把 bin 目录放进 PATH",
				versionText(installed), required[0], required[1])
		default:
			c.okf("Go %s（backend/go.mod 要求 >= %d.%d）", versionText(installed), required[0], required[1])
		}
	}

	if version, ok := c.commandVersion("Node", "node", "--version"); ok {
		if installed := parseVersion(version); installed == nil {
			c.warnf("认不出 Node 版本 %q，跳过版本判断", version)
		} else if !nodeSatisfies(installed) {
			c.missingf("Node %s 低于 Vite 8 要求的 20.19（或 22.12 以上）：升级 Node", versionText(installed))
		} else {
			c.okf("Node %s（Vite 8 要求 >= 20.19 或 >= 22.12）", versionText(installed))
		}
	}

	if version, ok := c.commandVersion("npm", "npm", "--version"); ok {
		c.okf("npm %s", version)
	}
}

// checkGoDependencies proves the backend's modules are present and its packages
// compile: `go build` downloads anything missing, `go mod verify` re-hashes what
// is already cached.
func (c *checker) checkGoDependencies() {
	if _, err := exec.LookPath("go"); err != nil {
		c.infof("Go 不可用，跳过依赖检查")
		return
	}
	dir := filepath.Join(c.root, "backend")
	checks := []struct {
		label string
		args  []string
		ok    string
		hint  string
	}{
		{
			label: "go mod verify",
			args:  []string{"mod", "verify"},
			ok:    "go mod verify：本地模块缓存与 go.sum 一致",
			hint:  "依赖缓存与 go.sum 不一致：检查是否被本地改过，或清掉模块缓存重新下载（GOPROXY 要可用）",
		},
		{
			label: "go build ./...",
			args:  []string{"build", "./..."},
			ok:    "go build ./...：依赖齐全，backend 全部包可编译",
			hint:  "依赖缺失、GOPROXY/网络不通，或代码本身编译不过；看下面输出的第一条错误",
		},
	}
	for _, check := range checks {
		c.infof("正在运行 %s（首次可能要下载依赖）…", check.label)
		output, err := runCommand(dir, 8*time.Minute, "go", check.args...)
		if err != nil {
			c.missingf("%s 失败：%s%s", check.label, check.hint, tailText(output, 8))
			continue
		}
		c.okf("%s", check.ok)
	}
}

// checkFrontendDependencies asks npm whether the installed tree still matches
// package.json and the lockfile. It reads only; nothing is installed.
func (c *checker) checkFrontendDependencies() {
	dir := filepath.Join(c.root, "frontend")
	if !isFile(filepath.Join(dir, "package-lock.json")) {
		c.missingf("frontend/package-lock.json 不存在：先执行 npm install 生成锁文件")
		return
	}
	if _, err := exec.LookPath("npm"); err != nil {
		c.infof("npm 不可用，跳过前端依赖检查")
		return
	}
	c.infof("正在运行 npm ls --all …")
	output, err := runCommand(dir, 3*time.Minute, "npm", "ls", "--all")
	if err != nil {
		c.missingf("npm ls --all 失败：已安装的包与 package.json/锁文件不一致%s", tailText(output, 8))
		return
	}
	c.okf("npm ls --all：node_modules 与 package.json、锁文件一致")
}

// commandVersion runs a tool and returns its version string. A missing tool is a
// missing environment, not a crash.
func (c *checker) commandVersion(label, name string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		c.missingf("%s 不可用（执行 %q 失败）：安装后把可执行文件所在目录加入 PATH", label, name)
		return "", false
	}
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
	if label == "Go" {
		// "go version go1.26.3 windows/amd64" → "1.26.3"
		if fields := strings.Fields(line); len(fields) >= 3 {
			line = strings.TrimPrefix(fields[2], "go")
		}
	}
	return strings.TrimPrefix(line, "v"), true
}

// checkDotenv compares backend/.env against the required keys declared in the
// backend's own source, so the two cannot drift apart. It returns the merged
// settings the service checks use: real values first, template values as the
// fallback when .env is missing or incomplete.
func (c *checker) checkDotenv() map[string]string {
	configPath := filepath.Join(c.root, "backend", "internal", "config", "config.go")
	example, exampleOrder, err := readEnvFile(filepath.Join(c.root, "backend", ".env.example"))
	if err != nil {
		c.missingf("读不到 backend/.env.example：%v", err)
		example = map[string]string{}
	}

	required, err := stringLiteralsBetween(configPath, "var requiredKeys = []string{", "\n}")
	if err != nil || len(required) == 0 {
		c.warnf("没能从 config.go 读出必填键，改用 .env.example 的全部键判断：%v", err)
		required = slices.DeleteFunc(slices.Clone(exampleOrder), func(key string) bool {
			return strings.HasPrefix(key, "VISION_")
		})
	}
	blankAllowed, _ := stringLiteralsBetween(configPath, "var blankAllowedKeys = map[string]bool{", "\n}")

	actual, _, err := readEnvFile(filepath.Join(c.root, "backend", ".env"))
	if err != nil {
		c.missingf("backend/.env 不存在或读不到：复制 backend/.env.example 为 backend/.env 后逐项填写")
		actual = map[string]string{}
	} else {
		c.okf("backend/.env 存在（%d 个键）", len(actual))
	}

	var missingKeys, emptyKeys, placeholderKeys []string
	for _, key := range required {
		value, present := actual[key]
		switch {
		case !present:
			missingKeys = append(missingKeys, key)
		case strings.TrimSpace(value) == "" && !slices.Contains(blankAllowed, key):
			emptyKeys = append(emptyKeys, key)
		case isPlaceholder(value):
			placeholderKeys = append(placeholderKeys, key)
		}
	}
	if len(missingKeys) == 0 && len(actual) > 0 {
		c.okf("backend/.env 覆盖了后端要求的 %d 个键", len(required))
	}
	if len(missingKeys) > 0 {
		c.missingf("backend/.env 缺少 %d 个键：%s", len(missingKeys), strings.Join(missingKeys, ", "))
	}
	if len(emptyKeys) > 0 {
		c.missingf("backend/.env 里这些键是空的：%s", strings.Join(emptyKeys, ", "))
	}
	if len(placeholderKeys) > 0 {
		c.missingf("backend/.env 里这些键仍是模板占位值，要填真实值：%s", strings.Join(placeholderKeys, ", "))
	}

	// Optional keys (VISION_*) need not exist; a leftover placeholder only matters
	// if the corresponding feature is used.
	for _, key := range exampleOrder {
		if slices.Contains(required, key) {
			continue
		}
		if value, present := actual[key]; present && isPlaceholder(value) {
			c.warnf("可选键 %s 仍是模板占位值，用到对应功能时会失败", key)
		}
	}

	merged := map[string]string{}
	for key, value := range example {
		merged[key] = value
	}
	for key, value := range actual {
		merged[key] = value
	}
	return merged
}

// checkDatabase proves the service can reach seekdb and that the schema it
// expects is already applied. `migrate status` reads .env, ensures the database
// exists (IF NOT EXISTS, the same thing startup does), and reads the migration
// table; it never applies anything.
func (c *checker) checkDatabase(values map[string]string) {
	host := strings.TrimSpace(values["DB_HOST"])
	if host == "" {
		host = "127.0.0.1"
	}
	port := strings.TrimSpace(values["DB_PORT"])
	if port == "" {
		port = "2881"
	}
	dbAddr := net.JoinHostPort(host, port)
	if err := dialTCP(dbAddr); err != nil {
		c.missingf("seekdb %s 连不上：先启动本地实例（例如 docker start seekdb）", dbAddr)
		return
	}
	c.okf("seekdb %s 可连接", dbAddr)
	if _, err := exec.LookPath("go"); err != nil {
		c.infof("Go 不可用，跳过迁移状态检查")
		return
	}

	c.infof("正在读取迁移状态 …")
	output, err := runCommand(filepath.Join(c.root, "backend"), 3*time.Minute, "go", "run", "./cmd/migrate", "status")
	if err != nil {
		c.missingf("读不到迁移状态：检查 .env 的数据库配置、库权限与 seekdb 状态%s", tailText(output, 6))
		return
	}
	pending, latest := 0, ""
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if latest == "" || fields[1] > latest {
			latest = fields[1]
		}
		if !strings.EqualFold(fields[0], "applied") {
			pending++
		}
	}
	switch {
	case pending > 0:
		c.missingf("schema 有 %d 条迁移未应用：cd backend && go run ./cmd/migrate up", pending)
	case latest != "":
		c.okf("schema 已是最新（%s）", latest)
	default:
		c.warnf("迁移状态没有输出，跳过版本判断")
	}
}

// checkServices always probes docling when an address is configured. Whether an
// unreachable service blocks startup depends on the parser engine: with
// PARSER_DEFAULT_ENGINE=docling it is required, otherwise it is only a warning
// because the backend will not call it.
func (c *checker) checkServices(values map[string]string) {
	engine := strings.ToLower(strings.TrimSpace(values["PARSER_DEFAULT_ENGINE"]))
	base := strings.TrimRight(strings.TrimSpace(values["PARSER_DOCLING_BASE_URL"]), "/")
	if base == "" {
		if engine == "docling" {
			c.missingf("PARSER_DEFAULT_ENGINE=docling 但 PARSER_DOCLING_BASE_URL 是空的")
		} else {
			c.warnf("PARSER_DOCLING_BASE_URL 是空的；当前解析引擎是 %q，用不到 docling", engine)
		}
		return
	}
	if err := httpHealthy(base + "/health"); err != nil {
		if engine == "docling" {
			c.missingf("docling %s/health 不可达：先启动 docling-serve（%v）", base, err)
		} else {
			c.warnf("docling %s/health 不可达（%v）；当前解析引擎是 %q，用不到它", base, err, engine)
		}
		return
	}
	c.okf("docling %s/health 可达（解析引擎 %q）", base, engine)
}

// readEnvFile parses the subset of dotenv this repository uses: KEY=VALUE,
// blank lines, # comments, an optional `export ` prefix, and one layer of
// matching quotes. It returns both the map and the file's key order.
func readEnvFile(path string) (map[string]string, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	values := map[string]string{}
	var order []string
	raw := strings.TrimPrefix(string(data), string(rune(0xFEFF)))
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, seen := values[key]; !seen {
			order = append(order, key)
		}
		values[key] = strings.Trim(strings.TrimSpace(value), "\"'")
	}
	return values, order, nil
}

func isPlaceholder(value string) bool {
	return strings.Contains(strings.TrimSpace(value), "replace-with")
}

// stringLiteralsBetween returns the quoted literals inside a block of Go source
// that starts at marker and ends at the next line that begins with "}".
func stringLiteralsBetween(path, marker, endMarker string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	source := string(data)
	start := strings.Index(source, marker)
	if start < 0 {
		return nil, fmt.Errorf("在 %s 里找不到 %q", filepath.Base(path), marker)
	}
	source = source[start+len(marker):]
	if end := strings.Index(source, endMarker); end >= 0 {
		source = source[:end]
	}
	pattern := regexp.MustCompile(`"([A-Z][A-Z0-9_]*)"`)
	keys := pattern.FindAllStringSubmatch(source, -1)
	if len(keys) == 0 {
		return nil, fmt.Errorf("%s 里的 %q 是空的", filepath.Base(path), marker)
	}
	out := make([]string, len(keys))
	for i, match := range keys {
		out[i] = match[1]
	}
	return out, nil
}

func requiredGoVersion(root string) []int {
	data, err := os.ReadFile(filepath.Join(root, "backend", "go.mod"))
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "go" {
			return parseVersion(fields[1])
		}
	}
	return nil
}

func parseVersion(raw string) []int {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "v")
	parts := strings.Split(raw, ".")
	var version []int
	for _, part := range parts {
		digits := part
		for i, r := range part {
			if r < '0' || r > '9' {
				digits = part[:i]
				break
			}
		}
		if digits == "" {
			break
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			break
		}
		version = append(version, n)
		if len(version) == 3 {
			break
		}
	}
	return version
}

func nodeSatisfies(version []int) bool {
	if len(version) < 2 {
		return false
	}
	major, minor := version[0], version[1]
	switch {
	case major == nodeMinMajor:
		return minor >= nodeMinMinor
	case major == 22:
		return minor >= 12
	default:
		return major > 22
	}
}

func versionText(version []int) string {
	parts := make([]string, len(version))
	for i, value := range version {
		parts[i] = strconv.Itoa(value)
	}
	return strings.Join(parts, ".")
}

func runCommand(dir string, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return strings.TrimSpace(output.String()), err
}

// tailText keeps a failure readable: only the last few lines of a long command.
func tailText(text string, lines int) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	parts := strings.Split(text, "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return "\n" + strings.Join(parts, "\n")
}

func dialTCP(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return err
	}
	return conn.Close()
}

func httpHealthy(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

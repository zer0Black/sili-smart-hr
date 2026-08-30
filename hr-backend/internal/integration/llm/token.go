package llm

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

// TokenCounter 可替换的计数实现，默认 tiktoken-go（specs §2.4 能力2）。
type TokenCounter interface {
	CountTokens(ctx context.Context, model string, messages []ChatMessage) (int, error)
}

// bpeDownloadTimeout 词表下载硬上限：SDK 默认 loader 裸 http.Get 无超时，
// 网络黑洞下会持 tiktoken 全局写锁无界挂起，故经自定义 BpeLoader 加超时。
const bpeDownloadTimeout = 10 * time.Second

// init 注入带超时与本地缓存的 BpeLoader，规避 SDK 默认 loader 的无界 http.Get。
func init() {
	tiktoken.SetBpeLoader(timedBpeLoader{})
}

// timedBpeLoader 实现 tiktoken.BpeLoader：优先读 SDK 约定的本地缓存
// （TIKTOKEN_CACHE_DIR / DATA_GYM_CACHE_DIR / TempDir/data-gym-cache，sha1(url) 命名），
// miss 时带 bpeDownloadTimeout 下载并回写缓存，供离线部署预置词表（specs §4.3）。
type timedBpeLoader struct{}

func (timedBpeLoader) LoadTiktokenBpe(blobpath string) (map[string]int, error) {
	contents, err := readBpeCached(blobpath)
	if err != nil {
		return nil, err
	}
	return parseBpeRanks(contents)
}

// readBpeCached 读本地缓存，miss 时带超时下载并回写。
func readBpeCached(blobpath string) ([]byte, error) {
	cacheDir := strings.TrimSpace(os.Getenv("TIKTOKEN_CACHE_DIR"))
	if cacheDir == "" {
		cacheDir = strings.TrimSpace(os.Getenv("DATA_GYM_CACHE_DIR"))
	}
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "data-gym-cache")
	}
	cacheKey := fmt.Sprintf("%x", sha1.Sum([]byte(blobpath)))
	cachePath := filepath.Join(cacheDir, cacheKey)
	if contents, err := os.ReadFile(cachePath); err == nil {
		return contents, nil
	}

	contents, err := downloadBpe(blobpath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cacheDir, os.ModePerm); err != nil {
		return nil, fmt.Errorf("create tiktoken cache dir: %w", err)
	}
	tmp := cachePath + "." + fmt.Sprintf("%d", time.Now().UnixNano()) + ".tmp"
	if err := os.WriteFile(tmp, contents, 0o644); err != nil {
		return contents, nil // 回写失败不影响本次结果
	}
	if err := os.Rename(tmp, cachePath); err != nil {
		os.Remove(tmp)
	}
	return contents, nil
}

// downloadBpe 带超时下载词表，非 http(s) 路径按本地文件读。
func downloadBpe(blobpath string) ([]byte, error) {
	if !strings.HasPrefix(blobpath, "http://") && !strings.HasPrefix(blobpath, "https://") {
		return os.ReadFile(blobpath)
	}
	client := &http.Client{Timeout: bpeDownloadTimeout}
	resp, err := client.Get(blobpath)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tiktoken bpe download: status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// parseBpeRanks 按行解析 BPE 词表：base64(token) rank。
func parseBpeRanks(contents []byte) (map[string]int, error) {
	ranks := make(map[string]int)
	for _, line := range strings.Split(string(contents), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, " ")
		if len(parts) != 2 {
			return nil, fmt.Errorf("tiktoken bpe line malformed: %q", line)
		}
		token, err := base64.StdEncoding.DecodeString(parts[0])
		if err != nil {
			return nil, err
		}
		rank, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, err
		}
		ranks[string(token)] = rank
	}
	return ranks, nil
}

// tiktokenCounter 默认计数实现：tiktoken-go 按模型取 BPE 编码计数，
// 未知模型或词表加载失败回退 rune 级启发式估算，不阻塞调用（specs §2.4 能力2 注意事项）。
type tiktokenCounter struct {
	fallback TokenCounter
}

// NewTikTokenCounter 返回默认实现。词表首载经 timedBpeLoader 带 10s 超时，
// 离线环境加载失败自动落到 fallback，生产可设 TIKTOKEN_CACHE_DIR 预置词表（specs §4.3）。
func NewTikTokenCounter() TokenCounter {
	return &tiktokenCounter{fallback: runeCounter{}}
}

func (c *tiktokenCounter) CountTokens(ctx context.Context, model string, messages []ChatMessage) (int, error) {
	tke, err := tiktoken.EncodingForModel(model)
	if err != nil {
		return c.fallback.CountTokens(ctx, model, messages)
	}
	total := 0
	for _, m := range messages {
		// EncodeOrdinary 不做 special token 检查，计数路径不会因原文触发 panic
		total += len(tke.EncodeOrdinary(m.Content))
	}
	return total, nil
}

// runeCounter 是 rune 级启发式回退计数实现，对输入无网络与词表依赖。
type runeCounter struct{}

func (runeCounter) CountTokens(_ context.Context, _ string, messages []ChatMessage) (int, error) {
	total := 0
	for _, m := range messages {
		total += len([]rune(m.Content))
	}
	return total, nil
}

// charDiv3Counter 是字符折算计数实现（1 token ≈ 3 字符，向上取整）：与调用方按
// 字符口径折算的截断预算配套使用，保证预检与截断同口径。上游非 OpenAI 系模型在
// tiktoken 映射表外，默认计数器会回退 rune 级 1:1 计数，字符折算预算（如
// budget = 2 × chars/3）会被逐字符口径系统性击穿，满额会话恒被预检拒绝。
type charDiv3Counter struct{}

// NewCharDiv3Counter 返回字符折算计数器，供预算与截断同以字符折算的调用方注入。
func NewCharDiv3Counter() TokenCounter { return charDiv3Counter{} }

func (charDiv3Counter) CountTokens(_ context.Context, _ string, messages []ChatMessage) (int, error) {
	total := 0
	for _, m := range messages {
		total += (len([]rune(m.Content)) + 2) / 3
	}
	return total, nil
}

// checkTokenBudget 调用前预检输入 token：TokenBudget<=0 跳过；
// 计数超预算返回 ErrContextLengthExceeded，不发起适配器调用（specs §2.4 能力2 / §5.1）。
func checkTokenBudget(ctx context.Context, budget int, counter TokenCounter, model string, messages []ChatMessage) error {
	if budget <= 0 {
		return nil // specs §2.2：默认 0 不预检
	}
	count, err := counter.CountTokens(ctx, model, messages)
	if err != nil {
		// 计数实现故障回退 rune 启发式，不把计数错误上抛阻塞调用
		count, err = runeCounter{}.CountTokens(ctx, model, messages)
		if err != nil {
			return err // runeCounter 恒 nil，此分支仅为签名完备
		}
	}
	if count > budget {
		return ErrContextLengthExceeded
	}
	return nil
}

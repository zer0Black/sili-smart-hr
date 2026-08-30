package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// 编译期断言：NewTikTokenCounter 返回值满足 TokenCounter 接口（specs §5.1 接口契约）。
var _ TokenCounter = NewTikTokenCounter()

// fakeCounter 测试替身，可编程返回值与错误，并记录调用次数（specs §5.1 token 预检）。
type fakeCounter struct {
	calls  int
	tokens int
	err    error
}

func (f *fakeCounter) CountTokens(_ context.Context, _ string, _ []ChatMessage) (int, error) {
	f.calls++
	return f.tokens, f.err
}

// TestCheckTokenBudgetOverflow fake 返回 40000，budget 32000 → ErrContextLengthExceeded。
func TestCheckTokenBudgetOverflow(t *testing.T) {
	fc := &fakeCounter{tokens: 40000}
	msgs := []ChatMessage{{Role: "user", Content: strings.Repeat("a", 100)}}
	err := checkTokenBudget(context.Background(), 32000, fc, "gpt-4o", msgs)
	if !errors.Is(err, ErrContextLengthExceeded) {
		t.Errorf("err = %v, want ErrContextLengthExceeded", err)
	}
	if fc.calls != 1 {
		t.Errorf("counter calls = %d, want 1", fc.calls)
	}
}

// TestCheckTokenBudgetSkip budget 0（默认不预检，specs §2.2/§3.1）→ 直接 nil，counter 不被调用。
func TestCheckTokenBudgetSkip(t *testing.T) {
	fc := &fakeCounter{tokens: 40000}
	msgs := []ChatMessage{{Role: "user", Content: "x"}}
	err := checkTokenBudget(context.Background(), 0, fc, "gpt-4o", msgs)
	if err != nil {
		t.Errorf("err = %v, want nil（budget 0 不预检）", err)
	}
	if fc.calls != 0 {
		t.Errorf("counter calls = %d, want 0（跳过预检不应计数）", fc.calls)
	}
}

// TestCheckTokenBudgetWithin fake 返回 1000，budget 32000 → nil。
func TestCheckTokenBudgetWithin(t *testing.T) {
	fc := &fakeCounter{tokens: 1000}
	msgs := []ChatMessage{{Role: "user", Content: "hello"}}
	err := checkTokenBudget(context.Background(), 32000, fc, "gpt-4o", msgs)
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

// TestCheckTokenBudgetCounterErrorFallback counter 报错 → 回退 runeCounter；
// rune 数超预算时返 ErrContextLengthExceeded，计数错误不上抛（specs §2.4 能力2）。
func TestCheckTokenBudgetCounterErrorFallback(t *testing.T) {
	fc := &fakeCounter{err: errors.New("boom")}
	msgs := []ChatMessage{
		{Role: "system", Content: strings.Repeat("中", 40000)},
	}
	err := checkTokenBudget(context.Background(), 32000, fc, "gpt-4o", msgs)
	if !errors.Is(err, ErrContextLengthExceeded) {
		t.Errorf("err = %v, want ErrContextLengthExceeded", err)
	}
	if fc.calls != 1 {
		t.Errorf("counter calls = %d, want 1", fc.calls)
	}
}

// TestCheckTokenBudgetCounterErrorFallbackWithin counter 报错且 rune 数未超预算 → nil，不返计数错误。
func TestCheckTokenBudgetCounterErrorFallbackWithin(t *testing.T) {
	fc := &fakeCounter{err: errors.New("boom")}
	msgs := []ChatMessage{{Role: "user", Content: "hello 中文"}}
	err := checkTokenBudget(context.Background(), 32000, fc, "gpt-4o", msgs)
	if err != nil {
		t.Errorf("err = %v, want nil（回退计数在预算内）", err)
	}
}

// TestCheckTokenBudgetEqualBoundary count 恰等于 budget → 未超限，返 nil（specs §2.4：超限才拦截）。
func TestCheckTokenBudgetEqualBoundary(t *testing.T) {
	fc := &fakeCounter{tokens: 32000}
	msgs := []ChatMessage{{Role: "user", Content: "x"}}
	if err := checkTokenBudget(context.Background(), 32000, fc, "gpt-4o", msgs); err != nil {
		t.Errorf("err = %v, want nil（等于预算不拦截）", err)
	}
}

// TestCheckTokenBudgetNegativeBudget budget 负数视为未启用预检（≤0 跳过）。
func TestCheckTokenBudgetNegativeBudget(t *testing.T) {
	fc := &fakeCounter{tokens: 999999}
	msgs := []ChatMessage{{Role: "user", Content: "x"}}
	if err := checkTokenBudget(context.Background(), -1, fc, "gpt-4o", msgs); err != nil {
		t.Errorf("err = %v, want nil（budget < 0 不预检）", err)
	}
	if fc.calls != 0 {
		t.Errorf("counter calls = %d, want 0", fc.calls)
	}
}

// TestCheckTokenBudgetEmptyMessages 空消息列表计数 0，预算 1 → nil。
func TestCheckTokenBudgetEmptyMessages(t *testing.T) {
	fc := &fakeCounter{tokens: 0}
	if err := checkTokenBudget(context.Background(), 1, fc, "gpt-4o", nil); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

// TestRuneCounterCounts runeCounter 对 3 条消息（含中文）的计数等于各块 Content 的 rune 数之和。
func TestRuneCounterCounts(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "你是人才测评专家"},       // 8
		{Role: "user", Content: "压缩以下会话为特征档案"},      // 12
		{Role: "assistant", Content: "hello world"}, // 11
	}
	want := len([]rune(msgs[0].Content)) + len([]rune(msgs[1].Content)) + len([]rune(msgs[2].Content))
	got, err := runeCounter{}.CountTokens(context.Background(), "unknown-model", msgs)
	if err != nil {
		t.Fatalf("CountTokens err = %v, want nil", err)
	}
	if got != want {
		t.Errorf("rune count = %d, want %d", got, want)
	}
	if want == 0 {
		t.Error("测试数据 rune 数不应为 0")
	}
}

// TestRuneCounterEmptyAndASCII 边界：空列表与空 Content 计数为 0，ASCII 同样按 rune 计。
func TestRuneCounterEmptyAndASCII(t *testing.T) {
	rc := runeCounter{}
	got, err := rc.CountTokens(context.Background(), "m", nil)
	if err != nil || got != 0 {
		t.Errorf("空消息: got=%d err=%v, want 0 nil", got, err)
	}
	got, err = rc.CountTokens(context.Background(), "m", []ChatMessage{{Role: "user", Content: ""}})
	if err != nil || got != 0 {
		t.Errorf("空 Content: got=%d err=%v, want 0 nil", got, err)
	}
	got, err = rc.CountTokens(context.Background(), "m", []ChatMessage{{Role: "user", Content: "abc"}})
	if err != nil || got != 3 {
		t.Errorf("ASCII: got=%d err=%v, want 3 nil", got, err)
	}
}

// TestNewTikTokenCounterNonNil NewTikTokenCounter 非 nil 且满足接口（编译期断言见文件头 var _）。
func TestNewTikTokenCounterNonNil(t *testing.T) {
	if NewTikTokenCounter() == nil {
		t.Error("NewTikTokenCounter() 不应返回 nil")
	}
}

// TestCharDiv3Counter 字符折算计数（1 token ≈ 3 字符，向上取整）：与按字符口径折算
// 截断预算的调用方（extractor）配套，保证预检与截断同口径。
func TestCharDiv3Counter(t *testing.T) {
	c := NewCharDiv3Counter()
	// 72000 rune 恰折算 24000 token（满额截断视图预算）。
	got, err := c.CountTokens(context.Background(), "deepseek-chat",
		[]ChatMessage{{Role: "user", Content: strings.Repeat("中", 72000)}})
	if err != nil || got != 24000 {
		t.Errorf("72000 rune: got=%d err=%v, want 24000 nil", got, err)
	}
	// 多消息各块独立向上取整：1+2 rune → 1+1 token。
	got, err = c.CountTokens(context.Background(), "glm-4",
		[]ChatMessage{{Role: "user", Content: "a"}, {Role: "assistant", Content: "ab"}})
	if err != nil || got != 2 {
		t.Errorf("1+2 rune: got=%d err=%v, want 2 nil", got, err)
	}
	// 空输入计 0。
	got, err = c.CountTokens(context.Background(), "m", nil)
	if err != nil || got != 0 {
		t.Errorf("空输入: got=%d err=%v, want 0 nil", got, err)
	}
}

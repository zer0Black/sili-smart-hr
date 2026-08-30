package extractor_test

import (
	"encoding/json"
	"strings"
	"testing"

	"sili-smart-hr/backend/internal/engine/extractor"
)

func TestRedact(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"Windows路径", `打开 D:\logs\app.log 看`, `打开 [PATH] 看`},
		{"Unix路径", "部署到 /home/user/app 下", "部署到 [PATH] 下"},
		{"Unix路径行首", "/usr/bin/env 是路径", "[PATH] 是路径"},
		{"sk密钥", "密钥 sk-abc123XYZdef 用这个", "密钥 [SECRET] 用这个"},
		{"内网地址10段与192段", "访问 10.20.30.40:8080 与 192.168.1.5", "访问 [ADDR]:8080 与 [ADDR]"},
		{"内网地址172段", "172.17.0.1 是容器网关", "[ADDR] 是容器网关"},
		{"混合四类一次全替换", "打开 D:\\logs\\app.log 看 /var/log/syslog 再用 sk-abc123XYZdef 访问 192.168.1.5", "打开 [PATH] 看 [PATH] 再用 [SECRET] 访问 [ADDR]"},
		// sk- 前导定界组：英文连字符词内嵌（sk- 前是字母/连字符）不误杀。
		{"连字符词task不误杀", "讨论 task-management 流程", "讨论 task-management 流程"},
		{"连字符词risk不误杀", "风格是 risk-averse 的", "风格是 risk-averse 的"},
		{"连字符词disk不误杀", "检查 disk-space 占用", "检查 disk-space 占用"},
		{"连字符词task-sk文件名不误杀", "看 task-sk-config.yaml", "看 task-sk-config.yaml"},
		{"连字符词disk-sk备份名不误杀", "disk-sk-backup 目录", "disk-sk-backup 目录"},
		{"独立短密钥仍拦", "独立 sk-abc123 一枚", "独立 [SECRET] 一枚"},
		{"sk后跟长密钥串仍拦", "key sk-AbCd1234EfGh here", "key [SECRET] here"},
		{"中文括号内密钥仍拦", "中文（sk-abc123XYZdef）括号", "中文（[SECRET]）括号"},
		{"行首密钥命中", "sk-AbCdEf123456 是密钥", "[SECRET] 是密钥"},
		{"中文后密钥命中", "密钥是 sk-AbCdEf123456", "密钥是 [SECRET]"},
		// Unix 路径：非空白定界符前导命中，中文斜杠短语不被吞。
		{"Unix路径等号前导", "path=/etc/nginx.conf 之一", "path=[PATH] 之一"},
		{"Unix路径括号内", "详见(/home/user/app) 目录", "详见([PATH]) 目录"},
		{"Unix路径file协议", "file:///usr/bin/env 即", "file://[PATH] 即"},
		{"Unix路径冒号前导", "看:/etc/hosts 去", "看:[PATH] 去"},
		{"Unix路径全角冒号前导", "日志位置：/var/log/app 下", "日志位置：[PATH] 下"},
		// 汉字定界：中文主场景路径常无标点分隔（配置在/usr/local 紧贴），汉字属定界类须命中（specs §5.1 路径只见占位符）。
		{"汉字紧贴路径", "配置在/etc/nginx/nginx.conf 改一下", "配置在[PATH] 改一下"},
		{"汉字紧贴路径usr", "日志在/usr/local 下面", "日志在[PATH] 下面"},
		{"汉字紧贴路径var", "看下/var/log/app.conf", "看下[PATH]"},
		{"Unix路径单段", "看 /bin 目录", "看 [PATH] 目录"},
		{"Unix路径末尾斜杠", "目录 /home/user/ 末尾", "目录 [PATH] 末尾"},
		{"中文斜杠短语不吞", "增加 /删除 选项", "增加 /删除 选项"},
		{"URL路径不吞", "访问 https://example.com/docs 查看", "访问 https://example.com/docs 查看"},
		{"URL汉字域名路径不吞", "见 https://中文.cn/docs 说明", "见 https://中文.cn/docs 说明"},
		{"普通斜杠词不吞", "TCP/IP 与 and/or 词", "TCP/IP 与 and/or 词"},
		{"字母紧贴斜杠不吞", "cat x/etc/hosts", "cat x/etc/hosts"},
		// Windows 路径体限 ASCII：中文紧贴盘符路径的正文不被吞（口语常无空白分隔）。
		{"Win路径中文紧贴不吞正文", "把D:\\logs\\app.log里的错误看看", "把[PATH]里的错误看看"},
		// 中文句读定界：句号/问号/顿号/逗号结尾后紧跟路径的形态命中。
		{"中文句号定界", "改好了。/etc/hosts 也同步改", "改好了。[PATH] 也同步改"},
		{"中文问号定界", "部署在哪？/usr/local/bin 下面", "部署在哪？[PATH] 下面"},
		// 数字边界：长数字串中段不误切、版本号不误判、多段数字不部分替换。
		{"长数字串中段不切", "1172.16.0.1 切一下", "1172.16.0.1 切一下"},
		{"版本号不误判", "版本v10.1.2.3发布", "版本v10.1.2.3发布"},
		{"多段数字不部分替换", "10.1.2.3.4.5 多段", "10.1.2.3.4.5 多段"},
		// 相邻地址：单字符分隔的第二地址前导组可续扫命中（组区间替换保分隔符）。
		{"空格相邻三地址全脱敏", "10.0.0.1 10.0.0.2 10.0.0.3", "[ADDR] [ADDR] [ADDR]"},
		{"逗号相邻三地址全脱敏", "192.168.1.1,192.168.1.2,192.168.1.3", "[ADDR],[ADDR],[ADDR]"},
		{"顿号相邻三地址全脱敏", "10.0.0.1、10.0.0.2、10.0.0.3", "[ADDR]、[ADDR]、[ADDR]"},
		{"地址尾随端口仍完整脱敏", "10.20.30.40:8080 与 10.20.30.41:9090", "[ADDR]:8080 与 [ADDR]:9090"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractor.Redact(tt.text, nil); got != tt.want {
				t.Errorf("Redact(%q, nil) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestRedactCustomPatterns(t *testing.T) {
	got := extractor.Redact("password=abc", []string{`(?i)password=\S+`})
	if got != "[REDACTED]" {
		t.Errorf("Redact(password=abc) = %q, want [REDACTED]", got)
	}
	got = extractor.Redact("连接串 password=abc123 已失效", []string{`(?i)password=\S+`})
	if got != "连接串 [REDACTED] 已失效" {
		t.Errorf("Redact(上下文) = %q, want 连接串 [REDACTED] 已失效", got)
	}
}

func TestRedactEmptyAndCleanText(t *testing.T) {
	if got := extractor.Redact("", nil); got != "" {
		t.Errorf("Redact(空文本) = %q, want 空串", got)
	}
	if got := extractor.Redact("今天天气不错", nil); got != "今天天气不错" {
		t.Errorf("Redact(无敏感串) = %q, want 原文", got)
	}
}

func TestRedactEmptyPatternsFallback(t *testing.T) {
	text := `打开 D:\logs\app.log 看`
	want := `打开 [PATH] 看`
	if got := extractor.Redact(text, []string{}); got != want {
		t.Errorf("Redact(空切片) = %q, want %q（空切片须与 nil 同走出厂回退）", got, want)
	}
}

func TestRedactInvalidPatternSkipped(t *testing.T) {
	got := extractor.Redact("a token=xyz b", []string{"[", `(?i)token=\S+`})
	if got != "a [REDACTED] b" {
		t.Errorf("Redact(含非法正则) = %q, want a [REDACTED] b（非法条目跳过不中断）", got)
	}
}

// TestRedactBlankPatternSkipped 回归：空串正则零宽匹配，注入分支若不跳过会把全文
// 逐位替换为占位符致文本乱形；误配条目跳过，其余正则仍生效。
func TestRedactBlankPatternSkipped(t *testing.T) {
	got := extractor.Redact("看下内部代号-9527的日志", []string{"", `内部代号-\d+`})
	if got != "看下[REDACTED]的日志" {
		t.Errorf("Redact(含空串条目) = %q, want 看下[REDACTED]的日志（空串跳过不乱形）", got)
	}
}

func TestRedactCustomOverridesDefault(t *testing.T) {
	got := extractor.Redact("密钥 sk-abc123XYZdef 与 password=abc", []string{`(?i)password=\S+`})
	want := "密钥 sk-abc123XYZdef 与 [REDACTED]"
	if got != want {
		t.Errorf("Redact(注入自定义集) = %q, want %q（注入集生效时出厂集整体让位）", got, want)
	}
}

// TestRedactSeededFactoryTyped 守护生产主路径占位符语义：migrateDB seed 落库的出厂集
// 读回（json 序列化往返）后注入 Redact，输出须与出厂回退分支同为类型化占位符
// [PATH]/[SECRET]/[ADDR]（specs §5.1 脱敏规则行与敏感串不进 LLM 上下文行：视图与
// 档案均只见分类占位符）。恒 [REDACTED] 会让 specs 承诺失效。
func TestRedactSeededFactoryTyped(t *testing.T) {
	seeded, err := json.Marshal(extractor.RedactPatterns())
	if err != nil {
		t.Fatalf("marshal RedactPatterns(): %v", err)
	}
	var patterns []string
	if err := json.Unmarshal(seeded, &patterns); err != nil {
		t.Fatalf("unmarshal seeded patterns: %v", err)
	}
	got := extractor.Redact("打开 D:\\logs\\app.log 看 /var/log 用 sk-abc123XYZdef 访问 192.168.1.5", patterns)
	want := "打开 [PATH] 看 [PATH] 用 [SECRET] 访问 [ADDR]"
	if got != want {
		t.Errorf("Redact(注入出厂集) = %q, want %q（注入出厂形态须保持类型化占位符）", got, want)
	}
	// 出厂集混入自定义条目：出厂条目仍类型化，自定义条目兜底 [REDACTED]。
	mixed := append(append([]string{}, patterns...), `(?i)password=\S+`)
	got = extractor.Redact("密钥 sk-abc123XYZdef 与 password=abc", mixed)
	want = "密钥 [SECRET] 与 [REDACTED]"
	if got != want {
		t.Errorf("Redact(出厂+自定义混集) = %q, want %q", got, want)
	}
}

// TestRedactFactoryAnchors 绝对锚点（BR4 口径迁自 migrate_test）：出厂 RedactPatterns()
// 必含覆盖 sk- 密钥与三段内网地址的字面量，删除任一类正则须红（specs §2.4 能力5）。
func TestRedactFactoryAnchors(t *testing.T) {
	joined := strings.Join(extractor.RedactPatterns(), "\n")
	for _, must := range []string{"sk-", `10\.`, `192\.168\.`, `172\.(?:1[6-9]|2\d|3[01])`} {
		if !strings.Contains(joined, must) {
			t.Errorf("RedactPatterns() missing %q（出厂脱敏覆盖被掏空）", must)
		}
	}
}

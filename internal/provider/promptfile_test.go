package provider

import (
	"os"
	"strings"
	"testing"
)

func TestPreparePromptForCli_SmallPrompt(t *testing.T) {
	small := "hello world"
	p := PreparePromptForCli(small, 0)
	defer p.Cleanup()

	if p.Prompt != small {
		t.Fatalf("expected original prompt returned, got %q", p.Prompt)
	}
}

func TestPreparePromptForCli_LargePrompt_WritesToTempFile(t *testing.T) {
	// 构造一个超过 100KB 的 prompt
	large := strings.Repeat("x", DefaultPromptSizeThreshold+1)

	p := PreparePromptForCli(large, 0)

	// Prompt 应该变成重定向指令，不再是原始内容
	if p.Prompt == large {
		t.Fatal("expected prompt to be replaced with redirect instruction, got original content")
	}
	if !strings.Contains(p.Prompt, "Read the file") {
		t.Fatalf("expected redirect instruction, got: %s", p.Prompt[:100])
	}

	// 从 redirect 指令中提取临时文件路径
	// 格式: "Read the file /tmp/codedebate-prompt-xxx.txt for ..."
	parts := strings.SplitN(p.Prompt, " ", 5) // "Read" "the" "file" "/tmp/..." "for..."
	if len(parts) < 4 {
		t.Fatalf("unexpected prompt format: %s", p.Prompt[:100])
	}
	tmpPath := parts[3]

	// 验证临时文件存在且内容完整
	content, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("temp file should exist at %s: %v", tmpPath, err)
	}
	if len(content) != len(large) {
		t.Fatalf("temp file size = %d, want %d", len(content), len(large))
	}
	if string(content) != large {
		t.Fatal("temp file content does not match original prompt")
	}

	// 调用 Cleanup，验证临时文件被删除
	p.Cleanup()
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("temp file should be deleted after Cleanup, but still exists: %s", tmpPath)
	}
}

func TestPreparePromptForCli_ExactThreshold_NoTempFile(t *testing.T) {
	// 刚好等于阈值，不应写临时文件
	exact := strings.Repeat("y", DefaultPromptSizeThreshold)
	p := PreparePromptForCli(exact, 0)
	defer p.Cleanup()

	if p.Prompt != exact {
		t.Fatal("prompt at exact threshold should be returned as-is")
	}
}

func TestPreparePromptForCli_CustomThreshold(t *testing.T) {
	// 用自定义阈值 50 字节
	prompt := strings.Repeat("z", 51)
	p := PreparePromptForCli(prompt, 50)

	if p.Prompt == prompt {
		t.Fatal("expected redirect for prompt exceeding custom threshold of 50 bytes")
	}
	if !strings.Contains(p.Prompt, "Read the file") {
		t.Fatalf("expected redirect instruction, got: %s", p.Prompt)
	}
	p.Cleanup()

	// 50 字节以内不触发
	small := strings.Repeat("z", 50)
	p2 := PreparePromptForCli(small, 50)
	defer p2.Cleanup()
	if p2.Prompt != small {
		t.Fatal("prompt at custom threshold should be returned as-is")
	}
}

// Package random 提供基于 crypto/rand 的随机字符串生成工具。
//
// 所有函数都在底层熵源失败时返回非 nil 的 error，绝不静默返回空串 ——
// 在认证 / token 场景里，调用方对"随机"的失败必须能感知到。
package random

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
)

// RandomString generates random string of specified length | 生成指定长度的随机字符串。
//
// length <= 0 时返回空字符串和 nil error。
// 底层 crypto/rand 失败时返回 ("", error)。
func RandomString(length int) (string, error) {
	if length <= 0 {
		return "", nil
	}

	// Calculate required byte length (base64 expands by ~33%)
	byteLen := (length * 3) / 4
	if byteLen < length {
		byteLen = length
	}

	bytes := make([]byte, byteLen)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("random: read entropy: %w", err)
	}

	encoded := base64.URLEncoding.EncodeToString(bytes)
	// Remove padding and trim to exact length
	encoded = strings.TrimRight(encoded, "=")
	if len(encoded) > length {
		return encoded[:length], nil
	}
	return encoded, nil
}

// RandomNumericString generates random numeric string | 生成随机数字字符串。
func RandomNumericString(length int) (string, error) {
	if length <= 0 {
		return "", nil
	}

	const digits = "0123456789"
	result := make([]byte, length)
	max := big.NewInt(int64(len(digits)))

	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("random: read entropy: %w", err)
		}
		result[i] = digits[n.Int64()]
	}

	return string(result), nil
}

// RandomAlphanumeric generates random alphanumeric string | 生成随机字母数字字符串。
func RandomAlphanumeric(length int) (string, error) {
	if length <= 0 {
		return "", nil
	}

	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, length)
	max := big.NewInt(int64(len(chars)))

	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("random: read entropy: %w", err)
		}
		result[i] = chars[n.Int64()]
	}

	return string(result), nil
}

package core

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// storeKey 把明文 token 派生为存储键（十六进制 SHA-256）。
//
// token 是高熵不透明凭证，无需加盐即可抵御彩虹表；以哈希作为 Store 的物理键、
// 且不把明文写入持久化值，确保数据库/Redis 被拖库或备份泄露时也无法还原 token。
// 哈希是单向的，不影响按 token 查找：Manager 在每个接收明文 token 的入口处统一
// 派生存储键，Store 始终只见哈希、从不接触明文。
//
// 注意：该转换是本库的存储边界约定——TokenState.Token 在库内部恒为存储键（哈希），
// 明文仅作为局部变量存在，只有在把状态交还外部调用方时才由 withPlaintextToken 注入。
func storeKey(token TokenValue) TokenValue {
	trimmed := strings.TrimSpace(string(token))
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return TokenValue(hex.EncodeToString(sum[:]))
}

// withPlaintextToken 返回一个把 Token 字段还原为明文的克隆，仅用于把状态交还给
// 外部调用方。
//
// 危险约束：该克隆绝不能再存回 Store —— 一旦以明文 Token 落库，本文件建立的
// “明文永不落库”不变量即被打破。库内部所有会重新保存状态的路径都必须使用
// Token 为哈希的原状态，而非本函数的返回值。
func withPlaintextToken(state *TokenState, plaintext TokenValue) *TokenState {
	if state == nil {
		return nil
	}
	clone := state.Clone()
	clone.Token = TokenValue(strings.TrimSpace(string(plaintext)))
	return clone
}

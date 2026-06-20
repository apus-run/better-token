package core

import "strings"

// LoginSubject 表示一个登录主体，即 token 与 session 在服务端归属的领域实体。
// 它由业务身份 LoginID 与登录类型 LoginType 共同组成，Store 以此作为
// 查找、删除登录态的领域语义键，而非具体的索引 key。
type LoginSubject struct {
	LoginID   string `json:"login_id"`
	LoginType string `json:"login_type"`
}

// Normalize 去除空白并在 LoginType 为空时回退到默认登录类型。
func (s LoginSubject) Normalize() LoginSubject {
	s.LoginID = strings.TrimSpace(s.LoginID)
	s.LoginType = strings.TrimSpace(s.LoginType)
	if s.LoginType == "" {
		s.LoginType = DefaultLoginType
	}
	return s
}

// IsZero 报告登录主体是否缺少业务身份。
func (s LoginSubject) IsZero() bool {
	return strings.TrimSpace(s.LoginID) == ""
}

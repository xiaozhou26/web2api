package main

import (
	"web2api/internal/pow"
)

// 公共 API:重新暴露 pow 包的类型/函数,保持外部 import 兼容。
type (
	// POWConfig 客户端指纹(由 internal/pow 提供实现)。
	POWConfig = pow.Config
	// TurnstileSolver Turnstile challenge solver 接口。
	TurnstileSolver = pow.TurnstileSolver
)

// NewPOWConfig 构造随机化的客户端指纹。
func NewPOWConfig(userAgent string) *POWConfig {
	return pow.NewConfig(userAgent)
}

// SolveProofToken 求解 proof token(header 用)。
func SolveProofToken(seed, difficulty, userAgent string) string {
	return pow.SolveProofToken(seed, difficulty, userAgent)
}

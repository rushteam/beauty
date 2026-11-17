package security

import (
	"golang.org/x/crypto/bcrypt"
)

// DefaultPasswordCost 是 HashPassword 使用的 bcrypt cost。
const DefaultPasswordCost = bcrypt.DefaultCost

// HashPassword 用 bcrypt(DefaultPasswordCost)哈希密码,结果自带盐与 cost,可直接入库。
// 密码超过 72 字节返回 bcrypt.ErrPasswordTooLong。
func HashPassword(password string) (string, error) {
	return HashPasswordWithCost(password, DefaultPasswordCost)
}

// HashPasswordWithCost 以指定 cost(4~31)哈希密码。cost 每 +1 耗时翻倍。
func HashPasswordWithCost(password string, cost int) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CheckPassword 校验明文密码是否与 HashPassword 的结果匹配(内部常量时间比较)。
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

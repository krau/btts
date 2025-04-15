package utils

import (
	"crypto/md5"
	"encoding/hex"
)

func MD5Hash(str string) string {
	hash := md5.Sum([]byte(str))
	return hex.EncodeToString(hash[:])
}

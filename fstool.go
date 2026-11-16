package main

// FinalShell 密码解密 —— 移植自用户提供的 fstool.go：
// base64 → 前 8 字节 head 派生 DES 密钥（Java Random 复现）→ DES-ECB → PKCS5。
// 与 fstool.go 的唯一差别：不把明文/密钥打进日志。

import (
	"crypto/des"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"fmt"
)

// fsDecodePass 解密 FinalShell 的 password 字段。
func fsDecodePass(data string) (string, error) {
	if data == "" {
		return "", nil
	}
	buf, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", err
	}
	if len(buf) < 8+des.BlockSize {
		return "", fmt.Errorf("密文太短: %d 字节", len(buf))
	}
	head := buf[:8]
	d := buf[8:]
	bt, err := fsDESDecode(d, fsRanDomKey(head))
	if err != nil {
		return "", err
	}
	return string(bt), nil
}

// fsRanDomKey 由 head 8 字节复现 FinalShell 的 Java 随机密钥派生。
func fsRanDomKey(head []byte) []byte {
	r := newJavaRandom(int64(head[5])).nextInt(127)
	ks := int64(3680984568597093857) / int64(r)
	random := newJavaRandom(ks)
	t := int(head[0])
	for i := 0; i < t; i++ {
		random.nextLong()
	}
	n := random.nextLong()
	r2 := newJavaRandom(n)

	ld := []int64{
		int64(head[4]), r2.nextLong(), int64(head[7]), int64(head[3]),
		r2.nextLong(), int64(head[1]), random.nextLong(), int64(head[2]),
	}
	keyData := make([]byte, len(ld)*8)
	for i, l := range ld {
		binary.BigEndian.PutUint64(keyData[i*8:(i+1)*8], uint64(l))
	}
	sum := md5.Sum(keyData)
	return sum[:]
}

// javaRandom 模拟 Java 的 Random。
type javaRandom struct{ seed int64 }

func newJavaRandom(seed int64) *javaRandom {
	return &javaRandom{seed: (seed ^ 0x5DEECE66D) & ((1 << 48) - 1)}
}

func (r *javaRandom) next(bits int) int32 {
	r.seed = (r.seed*0x5DEECE66D + 0xB) & ((1 << 48) - 1)
	return int32(r.seed >> (48 - bits))
}

func (r *javaRandom) nextInt(n int) int {
	if n <= 0 {
		panic("n must be positive")
	}
	if (n & -n) == n {
		return int(int64(n) * int64(r.next(31)) >> 31)
	}
	bits := r.next(31)
	val := bits % int32(n)
	for bits-val+int32(n)-1 < 0 {
		bits = r.next(31)
		val = bits % int32(n)
	}
	return int(val)
}

func (r *javaRandom) nextLong() int64 {
	return (int64(r.next(32)) << 32) + int64(r.next(32))
}

// fsDESDecode 与 fstool.go 的 DESDecode 一致：DES-ECB + PKCS5。
// 与 fstool.go 的差别：去填充失败时按解密失败处理（迁移场景宁可失败
// 也不能把乱码当密码写进凭据库）。
func fsDESDecode(data, head []byte) ([]byte, error) {
	var key []byte
	switch {
	case len(head) < 8:
		key = make([]byte, 8)
		copy(key, head)
	case len(head) > 8:
		key = head[:8]
	default:
		key = head
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	if len(data)%des.BlockSize != 0 {
		return nil, fmt.Errorf("data length (%d) must be multiple of block size (%d)", len(data), des.BlockSize)
	}
	block, err := des.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("des cipher: %v", err)
	}
	decrypted := make([]byte, len(data))
	for i := 0; i < len(data); i += des.BlockSize {
		block.Decrypt(decrypted[i:], data[i:i+des.BlockSize])
	}
	return pkcs5Unpad(decrypted)
}

func pkcs5Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > len(data) || padding > des.BlockSize {
		return nil, fmt.Errorf("invalid padding size: %d", padding)
	}
	for i := len(data) - padding; i < len(data); i++ {
		if data[i] != byte(padding) {
			return nil, fmt.Errorf("invalid padding byte at position %d", i)
		}
	}
	return data[:len(data)-padding], nil
}

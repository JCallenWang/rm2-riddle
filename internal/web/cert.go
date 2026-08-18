package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"
)

// hostNames 是憑證固定會涵蓋的名稱;裝置的 IP 由 DHCP 決定,另外動態加入。
var hostNames = []string{"remarkable.local", "remarkable", "localhost"}

// ensureCert 確保 certPath/keyPath 有一張可用的自簽憑證。
// 憑證不存在、已過期,或裝置換了 IP(DHCP)導致 SAN 不涵蓋現有位址時,會重新產生。
// 用 ECDSA P-256:armv7 上產生金鑰幾乎瞬間完成(RSA 2048 要數秒)。
func ensureCert(certPath, keyPath string) error {
	ips := localIPs()
	if certOK(certPath, keyPath, ips) {
		return nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "rm2-scribe", Organization: []string{"rm2-scribe"}},
		NotBefore:    now.Add(-1 * time.Hour),
		NotAfter:     now.AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     hostNames,
		IPAddresses:  ips,
		// IsCA 讓這張憑證也能被手動安裝成受信任的根憑證(手機上「永久信任」用)
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}

	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return err
	}
	if err := writePEM(keyPath, "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return err
	}
	return nil
}

// certOK 檢查既有憑證是否還能用(未過期、涵蓋目前所有位址)。
func certOK(certPath, keyPath string, ips []net.IP) bool {
	pemData, err := os.ReadFile(certPath)
	if err != nil {
		return false
	}
	if _, err := os.Stat(keyPath); err != nil {
		return false
	}
	block, _ := pem.Decode(pemData)
	if block == nil {
		return false
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	if time.Now().After(c.NotAfter.Add(-24 * time.Hour)) {
		return false
	}
	for _, ip := range ips {
		found := false
		for _, have := range c.IPAddresses {
			if have.Equal(ip) {
				found = true
				break
			}
		}
		if !found {
			return false // DHCP 換位址了 → 重簽
		}
	}
	return true
}

// localIPs 回傳本機所有可用的 IPv4(含 127.0.0.1)。
func localIPs() []net.IP {
	out := []net.IP{net.ParseIP("127.0.0.1")}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := n.IP.To4()
		if ip4 == nil || ip4.IsLoopback() {
			continue
		}
		out = append(out, ip4)
	}
	return out
}

func writePEM(path, typ string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("寫入 %s 失敗: %w", path, err)
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: typ, Bytes: der})
}

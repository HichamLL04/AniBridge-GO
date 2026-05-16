package utils

import (
	"bufio"
	"crypto/subtle"
	"os"
	"strings"
)

func CheckBasic(username, password, wantUser, wantPass, htpasswd string) bool {
	if htpasswd != "" {
		return CheckHtpasswd(username, password, htpasswd)
	}
	return subtle.ConstantTimeCompare([]byte(username), []byte(wantUser)) == 1 &&
		subtle.ConstantTimeCompare([]byte(password), []byte(wantPass)) == 1
}

func CheckHtpasswd(username, password, path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), ":", 2)
		if len(parts) == 2 && parts[0] == username {
			return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(password)) == 1
		}
	}
	return false
}

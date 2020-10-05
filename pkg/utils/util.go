package utils

import (
	"fmt"
	"os"
)

func GetNodeIDFromClusterList(servers []string, id string) string {
	for i, addr := range servers {
		if id == addr {
			return fmt.Sprintf("placement-%d", i)
		}
	}
	return ""
}

func EnsureDir(dirName string) error {
	err := os.Mkdir(dirName, 0755)
	if err == nil || os.IsExist(err) {
		return nil
	} else {
		return err
	}
}

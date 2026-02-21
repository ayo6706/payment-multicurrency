package service

import "fmt"

func requireExactlyOne(rows int64, operation string) error {
	if rows != 1 {
		return fmt.Errorf("%s affected %d rows", operation, rows)
	}
	return nil
}

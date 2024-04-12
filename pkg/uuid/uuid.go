package uuid

import "github.com/gofrs/uuid/v5"

func New() string {
	uuid, _ := uuid.NewV7()
	return uuid.String()
}

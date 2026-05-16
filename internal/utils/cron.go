package utils

import "github.com/robfig/cron/v3"

func ValidateCron(expr string) error {
	_, err := cron.ParseStandard(expr)
	return err
}

package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
)

// getAccountRoleListFromCSV reads the account list CSV file line by line
// and validates each line using the validateCSVAccountRoleData function
func getAccountRoleListFromCSV(filename string) ([][]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var accountRoleList [][]string

	r := csv.NewReader(f)

	for {
		record, err := r.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			} else if parseError, ok := err.(*csv.ParseError); ok {
				return nil, fmt.Errorf("error when parsing csv in line %d column %d, %v", parseError.Line, parseError.Column, err)
			} else {
				return nil, err
			}
		}

		err = validateCSVAccountRoleData(record)
		if err != nil {
			return nil, err
		}

		accountRoleList = append(accountRoleList, record)
	}

	return accountRoleList, err
}

func validateCSVAccountRoleData(record []string) error {
	if record == nil {
		return fmt.Errorf("cannot read empty account role data")
	} else if len(record) == 0 {
		return fmt.Errorf("account role must contain some data")
	}

	accountId := record[0]

	switch {
	case len(record) != 2:
		return fmt.Errorf("validation failed for account %s, the number of data for this account is not 2 columns", accountId)
	case len(accountId) != 12:
		return fmt.Errorf("validation failed for account %s, the account id must be 12 characters", accountId)
	default:
		return nil
	}
}

// getRoleARN formats accountRoleData into AWS IAM Role ARN format
func getRoleARN(accountRoleData []string) string {
	accountId := accountRoleData[0]
	roleName := accountRoleData[1]

	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", accountId, roleName)

	return roleARN
}

// getRoleARN gets the AWS Account ID from the accountRoleData
func getAccountId(accountRoleData []string) string {
	return accountRoleData[0]
}

func writeRecordsToCSV(filename string, userList []outputUser) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)

	for _, user := range userList {
		record := []string{
			user.accountId,
			user.name,
		}
		for _, key := range user.accessKeys {
			record = append(record, key.keyId)
			record = append(record, key.createdDate)
		}

		err := w.Write(record)
		if err != nil {
			return err
		}
	}

	w.Flush()
	err = w.Error()
	if err != nil {
		return err
	}

	return nil
}

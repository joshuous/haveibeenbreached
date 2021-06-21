package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
)

type Response events.APIGatewayProxyResponse

type AddAccountEvent struct {
	Accounts       []string
	PathParameters struct {
		BreachName string
	}
}

type Account struct {
	PK       string
	SK       string
	Type     string
	Account  string
	Breaches []string
}

var sess = session.Must(session.NewSessionWithOptions(session.Options{
	SharedConfigState: session.SharedConfigEnable,
}))
var svc = dynamodb.New(sess)
var tableName = "Breaches"
var entityType = "Account"

func Handler(ctx context.Context, event AddAccountEvent) (Response, error) {
	rawAccounts := event.Accounts
	breachName := event.PathParameters.BreachName

	accounts, err := mapToAccount(rawAccounts)
	if err != nil {
		return Response{StatusCode: 400, Body: fmt.Sprintf("Invalid email: %s", err)}, err
	}

	accounts, err = setAccountBreaches(accounts, breachName)
	if err != nil {
		return Response{StatusCode: 400, Body: fmt.Sprintf("Invalid email: %s", err)}, err
	}

	attrVals, err := marshalMapToAttributeValues(accounts)
	if err != nil {
		return Response{StatusCode: 400, Body: fmt.Sprintf("Error marshalling new Account: %s", err)}, err
	}

	for _, attrVal := range attrVals {
		input := &dynamodb.PutItemInput{
			Item:      attrVal,
			TableName: aws.String(tableName),
		}
		_, err = svc.PutItem(input)
		if err != nil {
			return Response{StatusCode: 400, Body: fmt.Sprintf("Error adding Account %+v to breach: %s", input, err)}, err
		}
	}

	numAccounts := len(accounts)
	body, err := json.Marshal(map[string]interface{}{
		"message": fmt.Sprintf("Successfully added/updated %d accounts to the %s breach.", numAccounts, breachName),
	})
	if err != nil {
		return Response{StatusCode: 400}, err
	}

	resp := Response{
		StatusCode:      200,
		IsBase64Encoded: false,
		Body:            string(body),
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
	}

	return resp, nil
}

func main() {
	lambda.Start(Handler)
}

func mapToAccount(accounts []string) ([]Account, error) {
	accs := make([]Account, 0, len(accounts))
	var err error

	for _, account := range accounts {
		email, emailErr := NewEmail(account)
		if err != nil {
			err = emailErr
			break
		}
		newAccount := Account{
			PK:       email.PartitionKey(),
			SK:       email.SortKey(),
			Type:     entityType,
			Account:  email.Account(),
			Breaches: make([]string, 0),
		}
		accs = append(accs, newAccount)
	}
	if err != nil {
		return []Account{}, err
	}
	return accs, nil
}

func setAccountBreaches(accounts []Account, breachName string) ([]Account, error) {
	accs := make([]Account, 0, len(accounts))
	var err error

	for _, account := range accounts {
		input := &dynamodb.GetItemInput{
			TableName: aws.String(tableName),
			Key: map[string]*dynamodb.AttributeValue{
				"PK": {
					S: aws.String(account.PK),
				},
				"SK": {
					S: aws.String(account.SK),
				},
			},
		}
		result, getItemErr := svc.GetItem(input)
		if getItemErr != nil {
			err = getItemErr
			break
		}
		if result.Item != nil {
			existingAcc := &Account{}
			unmarshalErr := dynamodbattribute.UnmarshalMap(result.Item, existingAcc)
			if unmarshalErr != nil {
				err = unmarshalErr
				break
			}
			var breaches []string
			if contains(existingAcc.Breaches, breachName) {
				breaches = existingAcc.Breaches
			} else {
				breaches = append(existingAcc.Breaches, breachName)
			}
			account.Breaches = breaches
		} else {
			account.Breaches = []string{breachName}
		}
		accs = append(accs, account)
	}
	if err != nil {
		return []Account{}, err
	}
	return accs, nil
}

func marshalMapToAttributeValues(accounts []Account) ([]map[string]*dynamodb.AttributeValue, error) {
	attrVals := make([]map[string]*dynamodb.AttributeValue, 0, len(accounts))
	var err error

	for _, account := range accounts {
		attrVal, marshalErr := dynamodbattribute.MarshalMap(account)
		if err != nil {
			err = marshalErr
			break
		}
		attrVals = append(attrVals, attrVal)
	}

	if err != nil {
		return []map[string]*dynamodb.AttributeValue{}, err
	}
	return attrVals, nil
}

var emailRegex = regexp.MustCompile("^[a-zA-Z0-9.!#$%&'*+\\/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")

type Email struct {
	Domain string
	Alias  string
}

func NewEmail(emailStr string) (Email, error) {
	if !emailRegex.MatchString(emailStr) {
		return Email{}, fmt.Errorf("not a valid email address: %s", emailStr)
	}
	email := strings.Split(emailStr, "@")
	return Email{
		Alias:  email[0],
		Domain: email[1],
	}, nil
}

func (e Email) Account() string {
	return fmt.Sprintf("%s@%s", e.Alias, e.Domain)
}

func (e Email) PartitionKey() string {
	return fmt.Sprintf("EMAIL#%s", e.Domain)
}

func (e Email) SortKey() string {
	return fmt.Sprintf("EMAIL#%s", e.Alias)
}

func contains(arr []string, str string) bool {
	for _, el := range arr {
		if el == str {
			return true
		}
	}
	return false
}

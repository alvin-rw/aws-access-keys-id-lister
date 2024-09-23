package main

import (
	"context"
	"flag"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"go.uber.org/zap"
)

type inputUser struct {
	iamClient *iam.Client
	name      string
	accountId string
}

type outputUser struct {
	name       string
	accountId  string
	accessKeys []accessKey
}

type accessKey struct {
	keyId       string
	createdDate string
}

// application struct holds the dependencies for the program
type application struct {
	logger *zap.Logger
}

// options holds the user custom parameters
type options struct {
	awsProfileName  string
	showDebug       bool
	accountListFile string
	outputFile      string
	numOfWorker     int
}

func main() {
	var opts options

	flag.StringVar(&opts.awsProfileName, "aws-profile", "default", "AWS CLI profile name")
	flag.BoolVar(&opts.showDebug, "debug", false, "Whether to show debug logs")
	flag.StringVar(&opts.accountListFile, "account-list-file", "accountlist.csv", "Account list file name")
	flag.StringVar(&opts.outputFile, "output-file", "output.csv", "Name of the output CSV file (default: output.csv)")
	flag.IntVar(&opts.numOfWorker, "workers", 10, "Number of worker to use")
	flag.Parse()

	logger := createLogger(opts.showDebug)
	defer logger.Sync()

	app := &application{
		logger: logger,
	}

	cfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion("us-east-1"), config.WithSharedConfigProfile(opts.awsProfileName))
	if err != nil {
		app.logger.Fatal("error when loading default config", zap.Error(err))
	}

	stsSvc := sts.NewFromConfig(cfg)

	csvAccountRoleList, err := getAccountRoleListFromCSV(opts.accountListFile)
	if err != nil {
		app.logger.Fatal("error when reading account list file", zap.Error(err))
	}

	inputUserList := []inputUser{}

	for _, csvAccountRole := range csvAccountRoleList {
		accountId := getAccountId(csvAccountRole)
		roleARN := getRoleARN(csvAccountRole)

		app.logger.Info("processing account",
			zap.String("account_id", accountId))

		tempCredentials, err := stsSvc.AssumeRole(context.Background(), &sts.AssumeRoleInput{
			RoleArn:         &roleARN,
			RoleSessionName: aws.String("aws-access-key-lister"),
		})
		if err != nil {
			app.logger.Fatal("error when doing assume role",
				zap.String("role_arn", roleARN),
				zap.Error(err),
			)
		}

		assumeRoleConfig, err := config.LoadDefaultConfig(context.Background(), config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				*tempCredentials.Credentials.AccessKeyId,
				*tempCredentials.Credentials.SecretAccessKey,
				*tempCredentials.Credentials.SessionToken,
			),
		))
		if err != nil {
			app.logger.Fatal("error when creating temporary config from the assumed role credentials",
				zap.String("role_arn", roleARN),
				zap.Error(err),
			)
		}

		assumeRoleIAMSvc := iam.NewFromConfig(assumeRoleConfig)

		listUsersInput := &iam.ListUsersInput{}

		app.logger.Debug("listing user account", zap.String("account_id", accountId))
		for {
			listUsersOutput, err := assumeRoleIAMSvc.ListUsers(context.Background(), listUsersInput)
			if err != nil {
				app.logger.Fatal("error when listing user", zap.Error(err))
			}

			for _, user := range listUsersOutput.Users {
				inputUserList = append(inputUserList, inputUser{
					name:      *user.UserName,
					accountId: accountId,
					iamClient: assumeRoleIAMSvc,
				})
			}

			if listUsersOutput.IsTruncated {
				listUsersInput.Marker = listUsersOutput.Marker
			} else {
				break
			}
		}
	}

	numberOfUsers := len(inputUserList)

	numOfWorker := opts.numOfWorker
	inputUserChan := make(chan inputUser, numberOfUsers)
	resultChan := make(chan *outputUser)

	for i := 1; i <= numOfWorker; i++ {
		go app.worker(i, inputUserChan, resultChan)
	}

	for i := 0; i < numberOfUsers; i++ {
		inputUserChan <- inputUserList[i]
	}
	close(inputUserChan)

	userList := []outputUser{}

	for i := 0; i < numberOfUsers; i++ {
		user := <-resultChan

		if user != nil {
			userList = append(userList, *user)
		}
	}

	err = writeRecordsToCSV(opts.outputFile, userList)
	if err != nil {
		app.logger.Fatal("error when writing records to CSV file",
			zap.Error(err),
		)
	}

}

// worker function will try to get access key for each user.
// If access keys are found, they will be added to the resultChan.
// If no access keys are found, worker will send nil to the resultChan.
func (app *application) worker(id int, inputUserChan <-chan inputUser, resultChan chan<- *outputUser) {
	for inputUser := range inputUserChan {
		outputAccessKeyList := []types.AccessKeyMetadata{}

		listAccessKeysInput := &iam.ListAccessKeysInput{
			UserName: &inputUser.name,
		}

		for {
			app.logger.Debug("listing access key for user",
				zap.Int("worker_id", id),
				zap.String("username", inputUser.name),
			)

			listAccessKeysOutput, err := inputUser.iamClient.ListAccessKeys(context.Background(), listAccessKeysInput)
			if err != nil {
				log.Fatalf("error when listing access key, %v", err) //TODO: handle error
			}

			app.logger.Debug("checking if we found access key",
				zap.Int("worker_id", id),
				zap.String("username", inputUser.name),
			)

			if len(listAccessKeysOutput.AccessKeyMetadata) != 0 {
				app.logger.Debug("access key found",
					zap.Int("worker_id", id),
					zap.String("username", inputUser.name),
				)

				outputAccessKeyList = append(outputAccessKeyList, listAccessKeysOutput.AccessKeyMetadata...)

				if listAccessKeysOutput.IsTruncated {
					listAccessKeysInput.Marker = listAccessKeysOutput.Marker
				} else {
					break
				}
			} else {
				app.logger.Debug("access key not found",
					zap.Int("worker_id", id),
					zap.String("username", inputUser.name),
				)

				break
			}
		}

		if len(outputAccessKeyList) != 0 {
			app.logger.Debug("putting user access key to the result channel",
				zap.Int("worker_id", id),
				zap.String("username", inputUser.name),
			)

			accessKeys := []accessKey{}
			for _, ak := range outputAccessKeyList {
				accessKeys = append(accessKeys, accessKey{
					keyId:       *ak.AccessKeyId,
					createdDate: ak.CreateDate.Format("2006-01-02T15:04:05-07:00"),
				})
			}

			resultChan <- &outputUser{
				name:       inputUser.name,
				accountId:  inputUser.accountId,
				accessKeys: accessKeys,
			}

			app.logger.Debug("finished putting the access key(s) into the result channel",
				zap.Int("worker_id", id),
				zap.String("username", inputUser.name),
			)
		} else {
			resultChan <- nil
		}
	}
}

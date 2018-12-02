package app

import (
	"errors"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/marcoalmeida/callme/task"
	"github.com/marcoalmeida/callme/util"
	"go.uber.org/zap"
)

const (
	defaultListenIP        = "0.0.0.0"
	defaultListenPort      = 6777
	defaultDynamoDBTable   = "callme-tasks"
	defaultDynamoDBRegion  = "us-east-1"
	defaultDynamoDBIndex   = "inverted_index"
	defaultConnectTimeout  = 1000
	defaultClientTimeout   = 3000
	defaultMaxRetires      = 3
	defaultCatchupInterval = 5
)

type CallMe struct {
	ListenIP         string `callme:"listen_ip"`
	ListenPort       int    `callme:"listen_port"`
	Debug            bool   `callme:"debug"`
	DynamoDBTable    string `callme:"dynamodb_table"`
	DynamoDBRegion   string `callme:"dynamodb_region"`
	DynamoDBIndex    string `callme:"dynamodb_index"`
	DynamoDBEndpoint string `callme:"dynamodb_endpoint"`
	ConnectTimeout   int    `callme:"connect_timeout"`
	ClientTimeout    int    `callme:"client_timeout"`
	MaxRetries       int    `callme:"max_retries"`
	CatchupInterval  int    `callme:"catchup_interval"`
	Logger           *zap.Logger
	ddb              *dynamodb.DynamoDB
	httpClient       *http.Client
}

// status of all tasks (submitted, running, succeeded, failed, attempted retries, return code/body from the callback)
type Status struct {
	Tasks []task.Task `json:"tasks"`
	// TODO: make this easier for the client, something that just be directly passed to the next call
	Next task.Task `json:"next"`
}

// New creates and returns a pointer to a new CallMe instance
func New(logger *zap.Logger) *CallMe {
	// set defaults
	cm := &CallMe{
		ListenIP:        defaultListenIP,
		ListenPort:      defaultListenPort,
		Debug:           false,
		DynamoDBTable:   defaultDynamoDBTable,
		DynamoDBRegion:  defaultDynamoDBRegion,
		DynamoDBIndex:   defaultDynamoDBIndex,
		ConnectTimeout:  defaultConnectTimeout,
		ClientTimeout:   defaultClientTimeout,
		MaxRetries:      defaultMaxRetires,
		CatchupInterval: defaultCatchupInterval,
		Logger:          logger,
	}

	// override configuration parameters with environment variables, if set
	t := reflect.TypeOf(*cm)
	v := reflect.ValueOf(cm).Elem()
	for i := 0; i < t.NumField(); i++ {
		// get the parameter name from the field tag
		param := strings.ToUpper(t.Field(i).Tag.Get("callme"))
		logger.Info("Reading configuration parameter", zap.String("parameter", param))
		value := os.Getenv(param)
		if value != "" {
			logger.Info("Found value", zap.String("parameter", param), zap.String("value", value))
			switch t.Field(i).Type.Kind() {
			case reflect.String:
				v.Field(i).SetString(value)
			case reflect.Int:
				n, err := strconv.Atoi(value)
				if err != nil {
					logger.Error(
						"Failed to convert integer",
						zap.String("param", param),
						zap.String("value", value))
					continue
				}
				v.Field(i).SetInt(int64(n))
			case reflect.Bool:
				if strings.ToLower(value) == "true" {
					v.Field(i).SetBool(true)
				}
			}
		}
	}

	// DynamoDB client
	cm.ddb = connectToDynamoDB(cm.DynamoDBRegion, cm.DynamoDBEndpoint, cm.MaxRetries)
	// initialize the HTTP client
	cm.httpClient = util.NewHTTPClient(cm.ConnectTimeout, cm.ClientTimeout)

	return cm
}

// Run continuously runs in the background and every minute executes the tasks scheduled for that minute
func (c *CallMe) Run() {
	for {
		currentMinute := util.GetUnixMinute()
		c.Logger.Debug("Calling back", zap.Int64("time", currentMinute))

		input := &dynamodb.QueryInput{
			TableName: aws.String(c.DynamoDBTable),
			ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
				":minute": {
					S: aws.String(strconv.FormatInt(currentMinute, 10)),
				},
			},
			KeyConditionExpression: aws.String("trigger_at = :minute"),
		}
		result, err := c.ddb.Query(input)
		if err != nil {
			c.Logger.Error(
				"Failed to Query tasks for the current minute",
				zap.Error(err),
				zap.Int64("current_minute", currentMinute),
			)
		} else {
			for _, item := range result.Items {
				tsk := c.taskFromDynamoDB(item)
				// TODO: worker pool
				go tsk.DoCallback(c.httpClient, c.UpsertTask, c.Logger)
			}
		}

		time.Sleep(time.Minute)
	}
}

// Catchup finds all entries in the past that have not run and replays them
// (if still within the maximum delay window). This could happen if the service is unavailable for a few minutes,
// for example.
func (c *CallMe) Catchup() {
	c.Logger.Info("Starting the catch up process")

	lastEvaluatedKey := make(map[string]*dynamodb.AttributeValue, 0)

	for {
		input := &dynamodb.ScanInput{
			TableName:      aws.String(c.DynamoDBTable),
			ConsistentRead: aws.Bool(false),
		}
		if len(lastEvaluatedKey) > 0 {
			input.ExclusiveStartKey = lastEvaluatedKey
		}
		// filter out future tasks: add an attribute value for the current time and
		// set a new condition expression that uses it
		input.ExpressionAttributeValues = map[string]*dynamodb.AttributeValue{
			":now": {
				S: aws.String(strconv.FormatInt(util.GetUnixMinute(), 10)),
			},
			":pending": {
				S: aws.String(task.Pending),
			},
		}
		input.FilterExpression = aws.String("trigger_at <= :now AND task_state = :pending")

		result, err := c.ddb.Scan(input)
		if err != nil {
			c.Logger.Error("Failed Scan while catching up", zap.Error(err))
			return
		} else {
			lastEvaluatedKey = result.LastEvaluatedKey
			// unmarshall and execute each task
			for _, i := range result.Items {
				t := task.Task{}
				err := dynamodbattribute.UnmarshalMap(i, &t)
				if err != nil {
					c.Logger.Error(
						"Failed to UnmarshalMap while catching up on a pending task",
						zap.Error(err),
						zap.String("task_name", *i["task_name"].S),
						zap.String("trigger_at", *i["trigger_at"].S),
					)
				} else {
					c.Logger.Debug("Catching up on pending task",
						zap.String("task", t.String()),
					)
					// TODO: worker pool
					go t.DoCallback(c.httpClient, c.UpsertTask, c.Logger)
				}
			}

			// we're done here
			if len(lastEvaluatedKey) == 0 {
				c.Logger.Info("Catch up process finished")
				return
			}
		}
	}
}

func (c *CallMe) CreateTask(t task.Task) (string, error) {
	c.Logger.Debug("Creating task", zap.String("task", t.String()))

	t.NormalizeTriggerAt()
	t.NormalizeTag()

	return c.UpsertTask(t)
}

// Reschedule creates new entries for tasks that failed. It may be applied to a specific instance of a give task,
// identified by name and time, or all instances that match a given name. If a new trigger time is not provided,
// it defaults to scheduling the tasks to the next minute.
// If the parameter all is set to true the tasks will be rescheduled regardless of whether or not the previous round
// succeeded.
func (c *CallMe) Reschedule(tsk task.Task, triggerAt string, all bool) ([]task.Task, error) {
	tasks := make([]task.Task, 0)

	if tsk.TriggerAt != "" && tsk.Tag != "" {
		// single task at a specific time -- we can re-use statusByTaskKey
		status, err := c.statusByTaskKey(tsk)
		if err != nil {
			return nil, err
		}

		// this will be a singleton; use it iff the task failed or we need to reschedule them all
		if status.Tasks[0].TaskState == task.Failed || all {
			tasks = status.Tasks
		}
	} else {
		// task identified by name, we need all its entries -- can re-use statusByTaskName and update all entries
		next := task.Task{}
		// collect all tasks
		for {
			result, err := c.statusByTaskName(tsk, next, false)
			if err != nil {
				return nil, err
			}

			for _, t := range result.Tasks {
				// reschedule only tasks that previously failed, unless explicitly asked to reschedule all
				if t.TaskState == task.Failed || all {
					tasks = append(tasks, t)
				}
			}

			// check to see if we're done here
			if result.Next == (task.Task{}) {
				break
			} else {
				next = result.Next
			}
		}
	}

	// update the trigger_at timestamp and upsert it to keep the exact same parameters we had before
	for i := 0; i < len(tasks); i++ {
		tasks[i].TriggerAt = triggerAt
		_, err := c.UpsertTask(tasks[i])
		if err != nil {
			return nil, err
		}
	}

	return tasks, nil
}

// Status returns the status of a specific task at a specific schedule,
// all entries of a given task (identified by its name),
// or all tasks currently scheduled. It supports pagination via startFrom and the next field in the returned JSON.
// It also allows to filter out all past entries if futureOnly is set to true.
func (c *CallMe) Status(tsk task.Task, startFrom task.Task, futureOnly bool) (Status, error) {
	// single task at a specific time -- we can collect the status with a simple call to GetItem
	if tsk.TriggerAt != "" && tsk.Tag != "" {
		return c.statusByTaskKey(tsk)
	}

	// single task, but all entries -- we can use the inverted index and Query the table, avoiding a Scan
	if tsk.Tag != "" {
		return c.statusByTaskName(tsk, startFrom, futureOnly)
	}

	// we have nothing to help us identify a unique entry or the set of entries for a given task
	// just return them all (paginated)
	return c.statusAllTasks(startFrom, futureOnly)
}

func (c *CallMe) statusByTaskKey(tsk task.Task) (Status, error) {
	status := Status{Tasks: make([]task.Task, 0)}

	input := &dynamodb.GetItemInput{
		TableName: aws.String(c.DynamoDBTable),
		Key: map[string]*dynamodb.AttributeValue{
			"trigger_at": {S: aws.String(tsk.TriggerAt)},
			"task_name":  {S: aws.String(tsk.Tag)},
		},
	}
	result, err := c.ddb.GetItem(input)
	if err != nil {
		c.Logger.Error(
			"Failed to get task status",
			zap.Error(err),
			zap.String("task_name", tsk.Tag),
			zap.String("trigger_at", tsk.TriggerAt))
		return Status{}, errors.New("failed to retrieve the task's status")
	}
	if len(result.Item) == 0 {
		return Status{}, errors.New("task not found")
	}

	// we found it, let's add it to the list and return
	status.Tasks = append(status.Tasks, c.taskFromDynamoDB(result.Item))

	return status, nil
}

// return the status of all entries for a given task, identified by name
// use the inverted index to call Query instead of doing a full table scan
func (c *CallMe) statusByTaskName(tsk task.Task, startFrom task.Task, futureOnly bool) (Status, error) {
	status := Status{Tasks: make([]task.Task, 0)}

	input := &dynamodb.QueryInput{
		TableName: aws.String(c.DynamoDBTable),
		IndexName: aws.String(c.DynamoDBIndex),
		ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
			":name": {
				S: aws.String(tsk.Tag),
			},
		},
		KeyConditionExpression: aws.String("task_name = :name"),
	}

	// filter out past tasks: add an attribute value for the current time and
	// set a new condition expression that uses it
	if futureOnly {
		input.ExpressionAttributeValues[":now"] = &dynamodb.AttributeValue{
			S: aws.String(strconv.FormatInt(time.Now().Unix(), 10)),
		}
		input.KeyConditionExpression = aws.String("task_name = :name AND trigger_at >= :now")
	}

	// we may be paginating this
	if startFrom.TriggerAt != "" && startFrom.Tag != "" {
		input.ExclusiveStartKey = map[string]*dynamodb.AttributeValue{
			"task_name":  {S: aws.String(startFrom.Tag)},
			"trigger_at": {S: aws.String(startFrom.TriggerAt)},
		}
	}

	result, err := c.ddb.Query(input)
	if err != nil {
		c.Logger.Error(
			"Failed to Query the status of a task by name",
			zap.Error(err),
			zap.String("task_name", tsk.Tag),
			zap.Bool("future_only", futureOnly),
		)
		return status, errors.New("failed to retrieve the task's status")
	}

	for _, item := range result.Items {
		tsk := c.taskFromDynamoDB(item)
		status.Tasks = append(status.Tasks, tsk)
	}

	// include the last evaluated key for pagination
	next := task.Task{}
	err = dynamodbattribute.UnmarshalMap(result.LastEvaluatedKey, &next)
	if err != nil {
		c.Logger.Error("Failed to UnmarshalMap last evaluated key", zap.Error(err))
	} else {
		status.Next = next
	}

	return status, nil
}

// scan the table
func (c *CallMe) statusAllTasks(startFrom task.Task, futureOnly bool) (Status, error) {
	status := Status{}

	// tasks in this table have not yet been executed (regardless of the trigger date)
	input := &dynamodb.ScanInput{
		TableName:      aws.String(c.DynamoDBTable),
		ConsistentRead: aws.Bool(false),
	}

	// filter out past tasks: add an attribute value for the current time and
	// set a new condition expression that uses it
	if futureOnly {
		input.ExpressionAttributeValues = map[string]*dynamodb.AttributeValue{
			":now": {
				S: aws.String(strconv.FormatInt(util.GetUnixMinute(), 10)),
			},
		}
		input.FilterExpression = aws.String("trigger_at > :now")
	}

	// we may be paginating this
	if startFrom.TriggerAt != "" && startFrom.Tag != "" {
		input.ExclusiveStartKey = map[string]*dynamodb.AttributeValue{
			"task_name":  {S: aws.String(startFrom.Tag)},
			"trigger_at": {S: aws.String(startFrom.TriggerAt)},
		}
	}

	result, err := c.ddb.Scan(input)
	if err != nil {
		c.Logger.Error("Failed to scan tasks table", zap.Error(err))
	} else {
		status.Tasks = make([]task.Task, 0)
		// collect the
		for _, i := range result.Items {
			t := task.Task{}
			err := dynamodbattribute.UnmarshalMap(i, &t)
			if err != nil {
				c.Logger.Error("Failed to UnmarshalMap on pending task", zap.Error(err))
			} else {
				c.Logger.Debug("Found pending task",
					zap.String("hash", *i["trigger_at"].S),
					zap.String("v", *i["task_name"].S),
				)
				status.Tasks = append(status.Tasks, t)
			}
		}
		// include the last evaluated key for pagination
		next := task.Task{}
		err := dynamodbattribute.UnmarshalMap(result.LastEvaluatedKey, &next)
		if err != nil {
			c.Logger.Error("Failed to UnmarshalMap last evaluated key", zap.Error(err))
		} else {
			status.Next = next
		}
	}

	return status, nil
}

// UpsertTask adds or replaces a task in DynamoDB. It returns a string that uniquely identifies
// the task and may be used to query its status or an error.
func (c *CallMe) UpsertTask(tsk task.Task) (string, error) {
	item, err := dynamodbattribute.MarshalMap(tsk)
	if err != nil {
		c.Logger.Error("Failed to update task on DynamoDB: MapMarshal", zap.Error(err))
		return "", errors.New("invalid JSON")
	}

	input := &dynamodb.PutItemInput{
		TableName: aws.String(c.DynamoDBTable),
		Item:      item,
	}
	_, err = c.ddb.PutItem(input)
	if err != nil {
		msg := "Failed to store task"
		c.Logger.Error(msg, zap.Error(err), zap.String("task", tsk.String()))
		return "", errors.New(strings.ToLower(msg))
	}

	c.Logger.Debug("Successfully upserted task", zap.String("task", tsk.String()))
	return tsk.UniqueID(), nil
}

// create a Task instance from a DynamoDB Item
func (c *CallMe) taskFromDynamoDB(item map[string]*dynamodb.AttributeValue) task.Task {
	tsk := task.Task{}

	err := dynamodbattribute.UnmarshalMap(item, &tsk)
	if err != nil {
		c.Logger.Error("Failed to unmarshal DynamoDB item into a task")
	}

	return tsk
}

func connectToDynamoDB(region string, endpoint string, maxRetries int) *dynamodb.DynamoDB {
	return dynamodb.New(session.Must(
		session.NewSession(
			aws.NewConfig().
				WithRegion(region).
				WithEndpoint(endpoint).
				WithMaxRetries(maxRetries),
		)))
}

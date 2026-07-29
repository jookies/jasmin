package restcompat

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

var relativeSchedule = regexp.MustCompile(`^(\d+)s$`)

type batchBuildError struct {
	title       string
	description string
}

func (err *batchBuildError) Error() string {
	return err.title + ": " + err.description
}

type batchJob struct {
	id          string
	delay       time.Duration
	tasks       []batchTask
	callbackURL string
	errbackURL  string
}

type batchTask struct {
	destination string
	body        []byte
}

type batchDispatcher struct {
	ctx            context.Context
	legacy         http.Handler
	callbackClient *http.Client
	throughput     float64
	smartQoS       bool
	queue          chan queuedBatchTask
}

type queuedBatchTask struct {
	job  batchJob
	task batchTask
}

func newBatchDispatcher(
	ctx context.Context,
	legacy http.Handler,
	callbackClient *http.Client,
	throughput float64,
	smartQoS bool,
) *batchDispatcher {
	if callbackClient == nil {
		callbackClient = http.DefaultClient
	}
	dispatcher := &batchDispatcher{
		ctx: ctx, legacy: legacy, callbackClient: callbackClient,
		throughput: throughput, smartQoS: smartQoS,
		queue: make(chan queuedBatchTask, 1024),
	}
	go dispatcher.work()
	return dispatcher
}

func buildBatch(payload map[string]json.RawMessage, username, password string) (batchJob, *batchBuildError) {
	id, err := newUUID()
	if err != nil {
		return batchJob{}, &batchBuildError{title: "Cannot create batch", description: err.Error()}
	}
	var globals map[string]json.RawMessage
	if raw, found := payload["globals"]; found {
		if err = json.Unmarshal(raw, &globals); err != nil || globals == nil {
			return batchJob{}, &batchBuildError{
				title: "Cannot parse globals", description: "globals must be a JSON object",
			}
		}
	}
	var configuration map[string]json.RawMessage
	if raw, found := payload["batch_config"]; found {
		if err = json.Unmarshal(raw, &configuration); err != nil || configuration == nil {
			return batchJob{}, &batchBuildError{
				title: "Cannot parse batch_config", description: "batch_config must be a JSON object",
			}
		}
	}
	delay, scheduleErr := parseSchedule(configuration["schedule_at"], time.Now())
	if scheduleErr != nil {
		return batchJob{}, scheduleErr
	}
	callbackURL, err := optionalString(configuration["callback_url"])
	if err != nil {
		return batchJob{}, &batchBuildError{title: "Cannot parse callback_url", description: err.Error()}
	}
	errbackURL, err := optionalString(configuration["errback_url"])
	if err != nil {
		return batchJob{}, &batchBuildError{title: "Cannot parse errback_url", description: err.Error()}
	}

	var messages []json.RawMessage
	if raw, found := payload["messages"]; found {
		if err = json.Unmarshal(raw, &messages); err != nil {
			return batchJob{}, &batchBuildError{
				title: "Cannot parse messages", description: "messages must be a JSON array",
			}
		}
	}
	job := batchJob{
		id: id, delay: delay, callbackURL: callbackURL, errbackURL: errbackURL,
	}
	for _, raw := range messages {
		var local map[string]json.RawMessage
		if err = json.Unmarshal(raw, &local); err != nil || local == nil {
			return batchJob{}, &batchBuildError{
				title: "Cannot parse messages", description: "every message must be a JSON object",
			}
		}
		merged := cloneRawMap(globals)
		for key, value := range local {
			merged[key] = value
		}
		_, hasContent := merged["content"]
		_, hasHex := merged["hex_content"]
		if !hasHex {
			_, hasHex = merged["hex-content"]
		}
		rawDestination, hasDestination := merged["to"]
		// The legacy batch facade silently ignores incomplete rows.
		if !hasDestination || (!hasContent && !hasHex) {
			continue
		}
		destinations, destinationErr := expandDestinations(rawDestination)
		if destinationErr != nil {
			return batchJob{}, &batchBuildError{title: "Cannot parse destination", description: destinationErr.Error()}
		}
		for _, destination := range destinations {
			message := cloneRawMap(merged)
			message["to"] = destination.raw
			translated := translatePayload(message, username, password)
			body, marshalErr := json.Marshal(translated)
			if marshalErr != nil {
				return batchJob{}, &batchBuildError{title: "Cannot encode message", description: marshalErr.Error()}
			}
			job.tasks = append(job.tasks, batchTask{destination: destination.text, body: body})
		}
	}
	return job, nil
}

type rawDestination struct {
	text string
	raw  json.RawMessage
}

func expandDestinations(raw json.RawMessage) ([]rawDestination, error) {
	var list []json.RawMessage
	if len(raw) > 0 && raw[0] == '[' {
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("to must be a scalar or array")
		}
		result := make([]rawDestination, 0, len(list))
		for _, item := range list {
			text, err := scalarText(item)
			if err != nil {
				return nil, fmt.Errorf("to array values must be strings or numbers")
			}
			result = append(result, rawDestination{text: text, raw: append(json.RawMessage(nil), item...)})
		}
		return result, nil
	}
	text, err := scalarText(raw)
	if err != nil {
		return nil, fmt.Errorf("to must be a string, number or array")
	}
	return []rawDestination{{text: text, raw: append(json.RawMessage(nil), raw...)}}, nil
}

func scalarText(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err == nil {
		return number.String(), nil
	}
	return "", fmt.Errorf("not a scalar")
}

func parseSchedule(raw json.RawMessage, now time.Time) (time.Duration, *batchBuildError) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, &batchBuildError{
			title:       "Cannot parse scheduled_at value",
			description: "schedule_at must be a string",
		}
	}
	if match := relativeSchedule.FindStringSubmatch(value); match != nil {
		seconds, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || seconds > int64((time.Duration(1<<63-1))/time.Second) {
			return 0, &batchBuildError{
				title: "Cannot parse scheduled_at value", description: "relative schedule is out of range",
			}
		}
		return time.Duration(seconds) * time.Second, nil
	}
	scheduled, err := time.ParseInLocation("2006-01-02 15:04:05", value, now.Location())
	if err != nil {
		return 0, &batchBuildError{
			title:       "Cannot parse scheduled_at value",
			description: fmt.Sprintf("Got unknown format: %s, correct formats are 'YYYY-MM-DD mm:hh:ss' or number of seconds", value),
		}
	}
	if scheduled.Before(now) {
		return 0, &batchBuildError{
			title:       "Cannot schedule batch in past date",
			description: fmt.Sprintf("Invalid past date given: %s", scheduled.Format("2006-01-02 15:04:05")),
		}
	}
	return scheduled.Sub(now), nil
}

func optionalString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("value must be a string")
	}
	return value, nil
}

func cloneRawMap(source map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}

func (dispatcher *batchDispatcher) dispatch(job batchJob) {
	go func() {
		if job.delay > 0 {
			timer := time.NewTimer(job.delay)
			defer timer.Stop()
			select {
			case <-dispatcher.ctx.Done():
				return
			case <-timer.C:
			}
		}
		for _, task := range job.tasks {
			select {
			case <-dispatcher.ctx.Done():
				return
			case dispatcher.queue <- queuedBatchTask{job: job, task: task}:
			}
		}
	}()
}

func (dispatcher *batchDispatcher) work() {
	currentThroughput := float64(0)
	var lastCompleted time.Time
	var lastRequestDuration time.Duration
	for {
		select {
		case <-dispatcher.ctx.Done():
			return
		case queued := <-dispatcher.queue:
			if currentThroughput > 0 && !lastCompleted.IsZero() {
				minimumDelay := time.Duration(float64(time.Second) / currentThroughput)
				remaining := minimumDelay - time.Since(lastCompleted)
				if remaining > 0 {
					timer := time.NewTimer(remaining)
					select {
					case <-dispatcher.ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
			}
			started := time.Now()
			dispatcher.run(queued.job, queued.task)
			elapsed := time.Since(started)
			lastCompleted = time.Now()

			if currentThroughput == 0 && dispatcher.throughput > 0 {
				currentThroughput = dispatcher.throughput
			}
			if dispatcher.smartQoS {
				switch {
				case elapsed > lastRequestDuration:
					if currentThroughput > 0 && currentThroughput*0.9 > 0 {
						currentThroughput *= 0.9
					} else if currentThroughput == 0 {
						currentThroughput = 0.5
					}
				case elapsed < lastRequestDuration && currentThroughput > 0:
					if dispatcher.throughput > 0 && currentThroughput*1.1 <= dispatcher.throughput {
						currentThroughput *= 1.1
					} else if dispatcher.throughput == 0 {
						currentThroughput = 0
					}
				}
			}
			lastRequestDuration = elapsed
		}
	}
}

func (dispatcher *batchDispatcher) run(job batchJob, task batchTask) {
	select {
	case <-dispatcher.ctx.Done():
		return
	default:
	}
	request, err := http.NewRequestWithContext(dispatcher.ctx, http.MethodPost, "/send", bytes.NewReader(task.body))
	if err != nil {
		go dispatcher.callback(job.errbackURL, job.id, task.destination, 0, "Unknown error: "+err.Error())
		return
	}
	request.Header.Set("Content-Type", jsonContentType)
	response := newCapture()
	dispatcher.legacy.ServeHTTP(response, request)
	if response.statusCode() != http.StatusOK {
		go dispatcher.callback(job.errbackURL, job.id, task.destination, 0, "HTTPAPI error: "+response.body.String())
		return
	}
	go dispatcher.callback(job.callbackURL, job.id, task.destination, 1, response.body.String())
}

func (dispatcher *batchDispatcher) callback(endpoint, batchID, destination string, status int, statusText string) {
	if endpoint == "" {
		return
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return
	}
	query := parsed.Query()
	query.Set("batchId", batchID)
	query.Set("to", destination)
	query.Set("status", strconv.Itoa(status))
	query.Set("statusText", statusText)
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(dispatcher.ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return
	}
	response, err := dispatcher.callbackClient.Do(request)
	if err == nil && response != nil {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}
}

func formatDelay(delay time.Duration) string {
	seconds := delay.Seconds()
	if seconds == float64(int64(seconds)) {
		return strconv.FormatInt(int64(seconds), 10) + "s"
	}
	return strconv.FormatFloat(seconds, 'f', -1, 64) + "s"
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" +
		encoded[16:20] + "-" + encoded[20:32], nil
}

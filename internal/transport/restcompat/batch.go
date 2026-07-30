package restcompat

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/pumpitspace/synevyr/internal/transport/httpcompat"
)

var relativeSchedule = regexp.MustCompile(`^(\d+)s$`)

type batchBuildError struct {
	title       string
	description string
	status      int
}

func (err *batchBuildError) Error() string {
	return err.title + ": " + err.description
}

type batchJob struct {
	id          string
	delay       time.Duration
	acceptedAt  time.Time
	tasks       []batchTask
	callbackURL string
	errbackURL  string
}

type batchTask struct {
	id               string
	destination      string
	username         string
	credentialDigest []byte
	body             []byte
}

type batchDispatcher struct {
	ctx                 context.Context
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
	legacy              http.Handler
	callbackClient      *http.Client
	store               BatchStore
	owner               string
	throughput          float64
	smartQoS            bool
	maxPending          int
	maxAttempts         int
	retryDelay          time.Duration
	callbackMaxAttempts int
	callbackRetryDelay  time.Duration
	lease               time.Duration
	wake                chan struct{}
}

func newBatchDispatcher(
	ctx context.Context,
	legacy http.Handler,
	callbackClient *http.Client,
	store BatchStore,
	throughput float64,
	smartQoS bool,
	maxPending int,
	maxAttempts int,
	retryDelay time.Duration,
	callbackMaxAttempts int,
	callbackRetryDelay time.Duration,
) (*batchDispatcher, error) {
	if callbackClient == nil {
		callbackClient = &http.Client{Timeout: 30 * time.Second}
	}
	if store == nil {
		store = NewMemoryBatchStore()
	}
	owner, err := newUUID()
	if err != nil {
		return nil, err
	}
	workerContext, cancel := context.WithCancel(ctx)
	dispatcher := &batchDispatcher{
		ctx: workerContext, cancel: cancel, legacy: legacy, callbackClient: callbackClient,
		store: store, owner: "rest-batch-" + owner,
		throughput: throughput, smartQoS: smartQoS, maxPending: maxPending,
		maxAttempts: maxAttempts, retryDelay: retryDelay,
		callbackMaxAttempts: callbackMaxAttempts, callbackRetryDelay: callbackRetryDelay,
		lease: 2 * time.Minute, wake: make(chan struct{}, 1),
	}
	if _, err = dispatcher.store.RecoverExpired(ctx, time.Now()); err != nil {
		cancel()
		return nil, fmt.Errorf("recover REST batch tasks: %w", err)
	}
	dispatcher.wg.Add(3)
	go func() {
		defer dispatcher.wg.Done()
		dispatcher.work()
	}()
	go func() {
		defer dispatcher.wg.Done()
		dispatcher.workCallbacks()
	}()
	go func() {
		defer dispatcher.wg.Done()
		dispatcher.recoverExpired()
	}()
	return dispatcher, nil
}

func (dispatcher *batchDispatcher) recoverExpired() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-dispatcher.ctx.Done():
			return
		case now := <-ticker.C:
			if recovered, err := dispatcher.store.RecoverExpired(dispatcher.ctx, now); err == nil && recovered > 0 {
				dispatcher.signal()
			}
		}
	}
}

func (dispatcher *batchDispatcher) close() {
	if dispatcher == nil {
		return
	}
	dispatcher.cancel()
	dispatcher.wg.Wait()
}

func buildBatch(
	payload map[string]json.RawMessage,
	username string,
	password string,
	maxTasks int,
) (batchJob, *batchBuildError) {
	now := time.Now()
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
	delay, scheduleErr := parseSchedule(configuration["schedule_at"], now)
	if scheduleErr != nil {
		return batchJob{}, scheduleErr
	}
	callbackURL, err := optionalString(configuration["callback_url"])
	if err != nil {
		return batchJob{}, &batchBuildError{title: "Cannot parse callback_url", description: err.Error()}
	}
	if err = validateCallbackURL(callbackURL); err != nil {
		return batchJob{}, &batchBuildError{title: "Cannot parse callback_url", description: err.Error()}
	}
	errbackURL, err := optionalString(configuration["errback_url"])
	if err != nil {
		return batchJob{}, &batchBuildError{title: "Cannot parse errback_url", description: err.Error()}
	}
	if err = validateCallbackURL(errbackURL); err != nil {
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
		id: id, delay: delay, acceptedAt: now, callbackURL: callbackURL, errbackURL: errbackURL,
	}
	credentialDigest := sha256.Sum256([]byte(password))
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
			if maxTasks > 0 && len(job.tasks) >= maxTasks {
				return batchJob{}, &batchBuildError{
					title:       "Batch queue is full",
					description: "The request expands beyond the configured durable batch backlog limit.",
					status:      http.StatusTooManyRequests,
				}
			}
			taskID, uuidErr := newUUID()
			if uuidErr != nil {
				return batchJob{}, &batchBuildError{title: "Cannot create batch task", description: uuidErr.Error()}
			}
			message := cloneRawMap(merged)
			message["to"] = destination.raw
			translated := translatePayload(message, username, password)
			// A delayed task carries a credential digest, never the password.
			// The internal HTTP handler receives an unforgeable trusted context
			// containing this digest and checks it against the current user.
			delete(translated, "password")
			body, marshalErr := json.Marshal(translated)
			if marshalErr != nil {
				return batchJob{}, &batchBuildError{title: "Cannot encode message", description: marshalErr.Error()}
			}
			job.tasks = append(job.tasks, batchTask{
				id: taskID, destination: destination.text, username: username,
				credentialDigest: append([]byte(nil), credentialDigest[:]...), body: body,
			})
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

func validateCallbackURL(value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil {
		return fmt.Errorf("value must be an absolute HTTP(S) URL")
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("value must be an absolute HTTP(S) URL without embedded credentials")
	}
	return nil
}

func cloneRawMap(source map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}

func (dispatcher *batchDispatcher) dispatch(job batchJob) error {
	availableAt := job.acceptedAt.Add(job.delay)
	stored := StoredBatch{
		ID: job.id, AcceptedAt: job.acceptedAt,
		CallbackURL: job.callbackURL, ErrbackURL: job.errbackURL,
		Tasks: make([]StoredTask, 0, len(job.tasks)),
	}
	for sequence, task := range job.tasks {
		stored.Tasks = append(stored.Tasks, StoredTask{
			ID: task.id, BatchID: job.id, Sequence: sequence + 1,
			Destination: task.destination, Username: task.username,
			CredentialDigest: append([]byte(nil), task.credentialDigest...),
			Body:             append([]byte(nil), task.body...), AvailableAt: availableAt,
		})
	}
	if err := dispatcher.store.CreateBatch(dispatcher.ctx, stored, dispatcher.maxPending); err != nil {
		return err
	}
	dispatcher.signal()
	return nil
}

func (dispatcher *batchDispatcher) work() {
	currentThroughput := float64(0)
	var lastCompleted time.Time
	var lastRequestDuration time.Duration
	for {
		task, err := dispatcher.store.ClaimTask(dispatcher.ctx, dispatcher.owner, time.Now(), dispatcher.lease)
		if errors.Is(err, ErrNoBatchWork) {
			if !dispatcher.wait() {
				return
			}
			continue
		}
		if err != nil {
			if !dispatcher.wait() {
				return
			}
			continue
		}
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
		dispatcher.run(task)
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

func (dispatcher *batchDispatcher) run(task StoredTask) {
	select {
	case <-dispatcher.ctx.Done():
		return
	default:
	}
	body := append([]byte(nil), task.Body...)
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		dispatcher.finishTask(task, false, 0, "Unknown error: corrupt durable task: "+err.Error())
		return
	}
	payload["password"] = mustJSON("__batch__")
	body, err := json.Marshal(payload)
	if err != nil {
		dispatcher.finishTask(task, false, 0, "Unknown error: "+err.Error())
		return
	}
	requestContext := httpcompat.WithTrustedBatchSubmit(
		dispatcher.ctx, task.Username, task.CredentialDigest, task.ID,
	)
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, "/send", bytes.NewReader(body))
	if err != nil {
		dispatcher.retryOrFinish(task, 0, "Unknown error: "+err.Error(), true)
		return
	}
	request.Header.Set("Content-Type", jsonContentType)
	response := newCapture()
	dispatcher.legacy.ServeHTTP(response, request)
	statusText := response.body.String()
	if response.statusCode() != http.StatusOK {
		dispatcher.retryOrFinish(
			task,
			response.statusCode(),
			"HTTPAPI error: "+statusText,
			retryableSubmitResponse(response.statusCode(), statusText),
		)
		return
	}
	dispatcher.finishTask(task, true, response.statusCode(), statusText)
}

func (dispatcher *batchDispatcher) retryOrFinish(
	task StoredTask,
	status int,
	statusText string,
	retryable bool,
) {
	if retryable && task.Attempt < dispatcher.maxAttempts {
		delay := exponentialBackoff(dispatcher.retryDelay, task.Attempt)
		if err := dispatcher.store.RetryTask(
			dispatcher.ctx, task.ID, dispatcher.owner, time.Now().Add(delay), statusText,
		); err == nil {
			dispatcher.signal()
			return
		}
	}
	dispatcher.finishTask(task, false, status, statusText)
}

func (dispatcher *batchDispatcher) finishTask(task StoredTask, successful bool, status int, statusText string) {
	callbackURL := task.ErrbackURL
	if successful {
		callbackURL = task.CallbackURL
	}
	if err := dispatcher.store.FinishTask(dispatcher.ctx, task.ID, dispatcher.owner, TaskResult{
		Successful: successful, HTTPStatus: status, StatusText: statusText, CallbackURL: callbackURL,
	}, time.Now()); err == nil {
		dispatcher.signal()
	}
}

func (dispatcher *batchDispatcher) workCallbacks() {
	for {
		callback, err := dispatcher.store.ClaimCallback(
			dispatcher.ctx, dispatcher.owner, time.Now(), dispatcher.lease,
		)
		if errors.Is(err, ErrNoBatchWork) {
			if !dispatcher.wait() {
				return
			}
			continue
		}
		if err != nil {
			if !dispatcher.wait() {
				return
			}
			continue
		}
		err = dispatcher.callback(callback)
		if err == nil {
			_ = dispatcher.store.FinishCallback(
				dispatcher.ctx, callback.TaskID, dispatcher.owner, time.Now(),
			)
			continue
		}
		if callback.Attempt >= dispatcher.callbackMaxAttempts {
			// Delivery is terminally abandoned after the configured attempts;
			// the task itself remains terminal and is never submitted again.
			_ = dispatcher.store.FinishCallback(
				dispatcher.ctx, callback.TaskID, dispatcher.owner, time.Now(),
			)
			continue
		}
		delay := exponentialBackoff(dispatcher.callbackRetryDelay, callback.Attempt)
		if retryErr := dispatcher.store.RetryCallback(
			dispatcher.ctx, callback.TaskID, dispatcher.owner, time.Now().Add(delay), err.Error(),
		); retryErr == nil {
			dispatcher.signal()
		}
	}
}

func (dispatcher *batchDispatcher) callback(callback StoredCallback) error {
	parsed, err := url.Parse(callback.URL)
	if err != nil {
		return err
	}
	query := parsed.Query()
	query.Set("batchId", callback.BatchID)
	query.Set("to", callback.Destination)
	query.Set("status", strconv.Itoa(callback.Status))
	query.Set("statusText", callback.StatusText)
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(dispatcher.ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err
	}
	response, err := dispatcher.callbackClient.Do(request)
	if err != nil {
		return err
	}
	if response == nil {
		return errors.New("callback client returned a nil response")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("callback returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (dispatcher *batchDispatcher) signal() {
	select {
	case dispatcher.wake <- struct{}{}:
	default:
	}
}

func (dispatcher *batchDispatcher) wait() bool {
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-dispatcher.ctx.Done():
		return false
	case <-dispatcher.wake:
		return true
	case <-timer.C:
		return true
	}
}

func retryableSubmitResponse(status int, body string) bool {
	return status == http.StatusTooManyRequests ||
		status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout ||
		(status == http.StatusForbidden && bytes.Contains([]byte(body), []byte("throughput exceeded")))
}

func exponentialBackoff(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		return 0
	}
	if attempt < 1 {
		attempt = 1
	}
	shift := min(attempt-1, 10)
	return base * time.Duration(1<<shift)
}

/*
	The old in-memory queue worker lived here. Durable task and callback claims
	now provide restart recovery and bounded admission, while preserving the same
	per-worker smart-QoS calculation above.
*/

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

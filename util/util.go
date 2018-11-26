package util

import (
	"bytes"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"runtime"
	"time"

	"go.uber.org/zap"
)

// GetCurrentMinuteUnix returns the Unix current timestamp with 1-minute resolution
func GetUnixMinute() int64 {
	now := time.Now().Unix()
	return now - now%60
}

// extract the name of the function that called the one that called this one
// benchmarks say ~1000ns/op
func getCaller(logger *zap.Logger) string {
	caller := ""

	// skip 2 frames because we need the name of the function calling this function
	pc, _, _, ok := runtime.Caller(2)
	if ok {
		details := runtime.FuncForPC(pc)
		if details != nil {
			caller = details.Name()
		} else {
			logger.Error("Failed to get details from runtime.FuncForPC")
		}
	} else {
		logger.Error("Failed to get PC from runtime.Caller")
	}

	return caller
}

// Backoff sleeps for random(0, 2^i*100) milliseconds and can be used for exponentially backing off by calling it with
// increasingly high values for i. The random factor is used to introduce jitter and avoid deterministic wait periods
// between retries. The parameter logger is a pointer to an already initialized instance of zap.Logger.
func Backoff(i int, logger *zap.Logger) {
	caller := getCaller(logger)
	if caller == "" {
		caller = "unknown"
	}

	// 2^i -- this will always be used for very small values (number of retries), so the signed/unsigned type casts
	// are safe
	var wait int64 = 1
	if i > 0 {
		wait = 2 << (uint64(i) - 1)
	}
	// multiples of 100ms
	wait *= 100
	// add jitter -- random(wait/2, wait)
	min := wait / 2
	wait = rand.Int63n(wait-min) + min
	logger.Debug("Exponential back off", zap.Int64("ms", wait), zap.String("caller", caller))
	time.Sleep(time.Duration(wait) * time.Millisecond)
}

// NewHTTPClient initializes and returns an HTTP client instance with proper connect and client timeout values
func NewHTTPClient(connectTimeout int, clientTimeout int) *http.Client {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   time.Duration(connectTimeout) * time.Millisecond,
			DualStack: true,
		}).DialContext,
	}

	return &http.Client{
		Transport: tr,
		Timeout:   time.Duration(clientTimeout) * time.Millisecond,
	}
}

func SendHTTPRequest(
	url string,
	payload []byte,
	headers http.Header,
	method string,
	client *http.Client,
	expectedStatusCode int,
	maxRetries int,
	logger *zap.Logger,
) (int, []byte) {
	// we always want to return the status and body, so it must exist outside of the scope of the for loop
	var status int
	var err error
	var req *http.Request
	var body []byte

	for i := 0; i < maxRetries; i++ {
		var resp *http.Response

		req, err = http.NewRequest(method, url, bytes.NewReader(payload))
		if err != nil {
			logger.Error("Failed to create HTTP request", zap.Error(err))
		}

		for k, values := range headers {
			for _, v := range values {
				req.Header.Add(k, v)
			}
		}

		if method == "POST" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}

		resp, err = client.Do(req)
		if err != nil {
			logger.Error(
				"Failed "+method,
				zap.Int("attempt", i),
				zap.Error(err),
			)
			Backoff(i, logger)
			continue
		}
		body, err = ioutil.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			logger.Error("Failed to read the response body", zap.Error(err))
			Backoff(i, logger)
			continue
		}

		if resp.StatusCode == expectedStatusCode {
			// success, we can stop here
			return resp.StatusCode, body
		} else {
			// client side error, no point on trying to continue
			if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
				return resp.StatusCode, body
			}
			// server side error, could be a number of things; we should wait and retry
			if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
				// save for return
				status = resp.StatusCode
				Backoff(i, logger)
			}
		}
	}

	// if we made it this far, the write failed
	// the status code will be 5XY or 0 (initialized as), depending on whether or not a connection was actually
	if err != nil {
		return status, []byte(err.Error())
	}

	return status, body
}

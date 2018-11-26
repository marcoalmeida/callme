package main

import (
	"fmt"
	"math/rand"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	"github.com/marcoalmeida/callme/app"
	"github.com/marcoalmeida/callme/handlers"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func initLogging() (*zap.Logger, *zap.AtomicLevel) {
	atom := zap.NewAtomicLevel()
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "timestamp"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	return zap.New(zapcore.NewCore(
			zapcore.NewJSONEncoder(encoderCfg),
			zapcore.Lock(os.Stdout),
			atom),
		),
		&atom
}

func main() {
	// logging
	logger, atom := initLogging()
	// flush the buffer before exiting
	defer logger.Sync()

	// parse the command line arguments
	app := app.New(logger)
	// set the requested log level
	if app.Debug {
		atom.SetLevel(zap.DebugLevel)
	}
	logger.Debug("Application configuration", zap.String("options", fmt.Sprintf("%+v", app)))

	// background task that will periodically scan the table for lost tasks
	// there are tasks that for some reason were never executed
	go app.Catchup()
	// background thread
	go app.Run()

	// listen and serve
	serve(app)
}

// setup handlers, ListenIP and serve ChronosDB
func serve(app *app.CallMe) {
	handlers.Register(app)

	app.Logger.Info(
		"Ready to ListenIP",
		zap.Int("ListenPort", app.ListenPort),
		zap.String("IP", app.ListenIP),
	)

	listenOn := fmt.Sprintf("%s:%d", app.ListenIP, app.ListenPort)
	err := http.ListenAndServe(listenOn, nil)
	if err != nil {
		app.Logger.Error("Server error", zap.Error(err))
	}
}

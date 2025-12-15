package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Log *zap.SugaredLogger

func Init() {
	config := zap.NewDevelopmentEncoderConfig()
	config.EncodeLevel = zapcore.CapitalColorLevelEncoder
	config.EncodeTime = zapcore.TimeEncoderOfLayout("15:04:05")
	config.EncodeCaller = nil

	encoder := zapcore.NewConsoleEncoder(config)
	core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), zapcore.DebugLevel)

	logger := zap.New(core)
	Log = logger.Sugar()
}

func Sync() {
	if Log != nil {
		Log.Sync()
	}
}

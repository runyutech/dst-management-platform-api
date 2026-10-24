package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Logger *zap.SugaredLogger
var AccessWriter *os.File
var RuntimeWriter *os.File

// AccessLog / RuntimeLog 是真正给 gin 和 zap 用的输出：文件和标准输出各写一份。
// 容器里（RGS / docker logs）只有 stdout 能看到，只写文件的话 .dmp 起来后就是一片安静
var (
	AccessLog  io.Writer
	RuntimeLog io.Writer
)

// syncWriter 同一个输出被多个 goroutine 同时写时按整条日志加锁，
// 不然 requests 一多，文件和 stdout 里的行会互相夹杂
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

var AccessFormatter = func(param gin.LogFormatterParams) string {
	return fmt.Sprintf(
		"[DMP] %s | %3d | %13v | %15s | %-7s %s\n",
		param.TimeStamp.Format("2006-01-02 15:04:05"),
		param.StatusCode,
		param.Latency,
		param.ClientIP,
		param.Method,
		param.Path,
	)
}

func InitLogger(level string) {
	var err error
	logDir := "logs"

	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		_ = os.MkdirAll(logDir, os.ModePerm)
	}

	RuntimeWriter, err = os.OpenFile(
		filepath.Join(logDir, "runtime.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0644,
	)
	if err != nil {
		panic("无法创建 runtime 日志文件: " + err.Error())
	}

	AccessWriter, err = os.OpenFile(
		filepath.Join(logDir, "access.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0644,
	)
	if err != nil {
		panic("无法创建 access 日志文件: " + err.Error())
	}

	// 文件照旧写，同时镜像到标准输出。容器里没有 tail 日志的习惯，
	// 面板控制台要能实时看到请求日志
	AccessLog = &syncWriter{w: io.MultiWriter(AccessWriter, os.Stdout)}
	RuntimeLog = &syncWriter{w: io.MultiWriter(RuntimeWriter, os.Stdout)}

	var zapLevel zapcore.Level
	switch strings.ToLower(level) {
	case "debug":
		zapLevel = zapcore.DebugLevel
	case "info":
		zapLevel = zapcore.InfoLevel
	case "warn":
		zapLevel = zapcore.WarnLevel
	case "error":
		zapLevel = zapcore.ErrorLevel
	default:
		zapLevel = zapcore.InfoLevel
	}

	// ✅ 仅在 debug 显示 caller
	var encodeCaller zapcore.CallerEncoder
	if zapLevel == zapcore.DebugLevel {
		encodeCaller = zapcore.ShortCallerEncoder
	}

	encoderConfig := zapcore.EncoderConfig{
		TimeKey:    "time",
		LevelKey:   "level",
		CallerKey:  "caller",
		MessageKey: "msg",

		EncodeTime: func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(t.Format("2006-01-02 15:04:05"))
		},

		EncodeLevel: func(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(fmt.Sprintf("[%s]", strings.ToUpper(l.String())))
		},

		EncodeCaller: encodeCaller,
	}

	runtimeCore := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(RuntimeLog),
		zapLevel,
	)

	Logger = zap.New(
		runtimeCore,
		zap.AddCaller(),
	).Sugar()

	gin.DefaultWriter = io.Discard
	gin.DefaultErrorWriter = io.Discard

	// 接管运行时崩溃输出，把未被捕获的 panic 日志也写入 runtime 日志
	initRuntimeCapture()
}

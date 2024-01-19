package zapld

import (
	"fmt"
	"github.com/LogDoc-org/logdoc-go-appender/common"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

var application string

var log *zap.Logger

var connection net.Conn

func GetLogger() *zap.Logger {
	return log
}

// In contexts where performance is nice, but not critical, we are using the SugaredLogger.
// It's 4-10x faster than other structured logging packages and includes both structured and printf-style APIs.

// When performance and type safety are critical, we are using the Logger.
// It's even faster than the SugaredLogger and allocates far less, but it only supports structured logging.
// https://github.com/uber-go/zap

func Init(config *zap.Config, initialLevel zapcore.Level, proto string, address string, app string, development bool) (net.Conn, error) {
	var cfg zap.Config

	if config == nil {
		// создаем конфигурацию логгера
		cfg = zap.Config{
			Development:      development,
			Encoding:         "json",
			Level:            zap.NewAtomicLevelAt(initialLevel),
			OutputPaths:      []string{"stdout"},
			ErrorOutputPaths: []string{"stderr"},
			EncoderConfig: zapcore.EncoderConfig{
				TimeKey:        "time",
				LevelKey:       "level",
				MessageKey:     "msg",
				EncodeTime:     zapcore.ISO8601TimeEncoder,
				EncodeLevel:    zapcore.CapitalLevelEncoder,
				EncodeDuration: zapcore.StringDurationEncoder,
			},
		}
	} else {
		cfg = *config
		cfg.Level.SetLevel(initialLevel)
	}

	logger, err := cfg.Build()
	if err != nil {
		log.Error("Ошибка создания конфигурации")
		return nil, err
	}

	logger = logger.WithOptions(zap.Hooks(sendLogDocEvent))

	application = app
	log = logger

	conn, err := networkWriter(proto, address)
	if err != nil {
		log.Error("Ошибка соединения с LogDoc сервером")
		return nil, err
	}

	connection = conn

	logger.Info("LogDoc subsystem initialized successfully")

	return connection, nil
}

func sendLogDocEvent(entry zapcore.Entry) error {
	header := []byte{6, 3}
	app := application
	var lvl string
	if strings.Compare(entry.Level.String(), "warning") == 0 {
		lvl = "warn"
	} else {
		lvl = entry.Level.String()
	}
	ip := connection.RemoteAddr().String()
	pid := fmt.Sprintf("%d", os.Getpid())
	src := entry.Caller.Function + ":" + strconv.Itoa(entry.Caller.Line)

	t := time.Now()
	tsrc := t.Format("060201150405.000") + "\n"

	// Пишем заголовок
	result := header
	// Записываем само сообщение
	common.WritePair("msg", entry.Message, &result)
	// Обрабатываем кастомные поля
	common.ProcessCustomFields(entry.Message, &result)
	// Служебные поля
	common.WritePair("app", app, &result)
	common.WritePair("tsrc", tsrc, &result)
	common.WritePair("lvl", lvl, &result)
	common.WritePair("ip", ip, &result)
	common.WritePair("pid", pid, &result)
	common.WritePair("src", src, &result)

	// Финальный байт, завершаем
	result = append(result, []byte("\n")...)

	_, err := connection.Write(result)
	if err != nil {
		log.Error("Ошибка записи в соединение, ", zap.Error(err))
	}
	return nil
}

func networkWriter(proto string, address string) (net.Conn, error) {
	switch {
	case proto == "tcp":
		return tcpWriter(address)
	case proto == "udp":
		return udpWriter(address)
	default:
		log.Error("Error connecting LogDoc server, ", zap.String("address", address))
		return nil, fmt.Errorf("error accessing LogDoc server, %s", address)
	}
}

// функция для создания TCP соединения и возврата io.Writer
func tcpWriter(address string) (net.Conn, error) {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		log.Error("Error connecting LogDoc server using tcp, ", zap.String("address", address), zap.Error(err))
		return nil, err
	}
	return conn, nil
}

// функция для создания UDP соединения и возврата io.Writer
func udpWriter(address string) (net.Conn, error) {
	conn, err := net.Dial("udp", address)
	if err != nil {
		log.Error("Error connecting LogDoc server using udp, ", zap.String("address", address), zap.Error(err))
		return nil, err
	}
	return conn, nil
}

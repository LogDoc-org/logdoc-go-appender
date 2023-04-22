package logdoc

import (
	"bytes"
	"fmt"
	"github.com/sirupsen/logrus"
	"net"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultAsyncBufferSize = 8192

var application string

var lgr *logrus.Logger

func GetLogger() *logrus.Logger {
	return lgr
}

type Hook struct {
	sync.RWMutex
	conn                     net.Conn
	protocol                 string
	address                  string
	appName                  string
	alwaysSentFields         logrus.Fields
	hookOnlyPrefix           string
	TimeFormat               string
	fireChannel              chan *logrus.Entry
	AsyncBufferSize          int
	WaitUntilBufferFrees     bool
	Timeout                  time.Duration // Timeout for sending message.
	MaxSendRetries           int           // Declares how many times we will try to resend message.
	ReconnectBaseDelay       time.Duration // First reconnect delay.
	ReconnectDelayMultiplier float64       // Base multiplier for delay before reconnect.
	MaxReconnectRetries      int           // Declares how many times we will try to reconnect.
}

func (h *Hook) Levels() []logrus.Level {
	return []logrus.Level{
		logrus.PanicLevel,
		logrus.FatalLevel,
		logrus.ErrorLevel,
		logrus.WarnLevel,
		logrus.InfoLevel,
		logrus.DebugLevel,
	}
}

// / Fire send message to logdoc.
// In async mode log message will be dropped if message buffer is full.
// If you want wait until message buffer frees – set WaitUntilBufferFrees to true.
func (h *Hook) Fire(entry *logrus.Entry) error {
	if h.fireChannel != nil { // Async mode.
		select {
		case h.fireChannel <- entry:
		default:
			if h.WaitUntilBufferFrees {
				h.fireChannel <- entry // Blocks the goroutine because buffer is full.
				return nil
			}
			// Drop message by default.
		}
		return nil
	}
	return h.sendMessage(entry)
}

func (h *Hook) sendMessage(entry *logrus.Entry) error {
	header := []byte{6, 3}
	app := application
	var lvl string
	if strings.Compare(entry.Level.String(), "warning") == 0 {
		lvl = "warn"
	} else {
		lvl = entry.Level.String()
	}
	ip := h.conn.RemoteAddr().String()
	pid := fmt.Sprintf("%d", os.Getpid())
	src := entry.Caller.Function + ":" + strconv.Itoa(entry.Caller.Line)

	t := time.Now()
	tsrc := t.Format("060201150405.000") + "\n"

	// Пишем заголовок
	result := header
	// Записываем само сообщение
	writePair("msg", entry.Message, &result)
	// Обрабатываем кастомные поля
	processCustomFields(entry.Message, &result)
	// Служебные поля
	writePair("app", app, &result)
	writePair("tsrc", tsrc, &result)
	writePair("lvl", lvl, &result)
	writePair("ip", ip, &result)
	writePair("pid", pid, &result)
	writePair("src", src, &result)

	// Финальный байт, завершаем
	result = append(result, []byte("\n")...)

	_, err := h.conn.Write(result)
	if err != nil {
		logrus.Errorf("Ошибка записи в соединение, %s", err.Error())
	}
	return nil
}

func Init(proto string, address string, app string) (net.Conn, error) {
	l := logrus.New()
	l.SetReportCaller(true)
	l.Formatter = &logrus.TextFormatter{
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			filename := path.Base(f.File)
			return fmt.Sprintf("%s:%d", filename, f.Line), fmt.Sprintf("%s()", f.Function)
		},
		DisableColors: false,
		FullTimestamp: true,
	}

	hook, conn, err := NewHook(proto, address)
	if err != nil {
		logrus.Error(err)
		return nil, err
	}

	l.AddHook(hook)

	l.SetLevel(logrus.DebugLevel)

	lgr = l

	application = app

	return conn, nil
}

func NewHook(protocol, address string) (*Hook, net.Conn, error) {
	conn, err := net.Dial(protocol, address)
	if err != nil {
		logrus.Error("Error connecting LogDoc server, ", address, err)
		return nil, nil, err
	}

	hook := &Hook{conn: conn, protocol: protocol, address: address}

	return hook, conn, nil
}

func (h *Hook) makeAsync() {
	if h.AsyncBufferSize == 0 {
		h.AsyncBufferSize = defaultAsyncBufferSize
	}
	h.fireChannel = make(chan *logrus.Entry, h.AsyncBufferSize)

	go func() {
		for entry := range h.fireChannel {
			if err := h.sendMessage(entry); err != nil {
				fmt.Println("Error during sending message to logdoc:", err)
			}
		}
	}()
}

func writePair(key string, value string, arr *[]byte) {
	sepIdx := strings.Index(value, "@@")
	msg := ""
	if sepIdx != -1 {
		msg = value[0:sepIdx]
	} else {
		msg = value
	}
	if strings.Index(msg, "\n") != -1 {
		writeComplexPair(key, msg, arr)
	} else {
		writeSimplePair(key, msg, arr)
	}
}

func writeComplexPair(key string, value string, arr *[]byte) {
	*arr = append(*arr, []byte(key)...)
	*arr = append(*arr, []byte("\n")...)
	*arr = append(*arr, writeInt(len(value))...)
	*arr = append(*arr, []byte(value)...)
}

func writeSimplePair(key string, value string, arr *[]byte) {
	*arr = append(*arr, []byte(key+"="+value+"\n")...)
}

func processCustomFields(msg string, arr *[]byte) {
	// Обработка кастом полей
	sepIdx := strings.Index(msg, "@@")
	rawFields := ""

	if sepIdx != -1 {
		rawFields = msg[sepIdx+2:]
		keyValuePairs := strings.Split(rawFields, "@")

		for _, pair := range keyValuePairs {
			keyValue := strings.Split(pair, "=")
			if len(keyValue) == 2 {
				*arr = append(*arr, []byte(keyValue[0]+"="+keyValue[1]+"\n")...)
			}
		}
	}
}

func writeInt(in int) []byte {
	buf := new(bytes.Buffer)
	buf.WriteByte(byte((in >> 24) & 0xff))
	buf.WriteByte(byte((in >> 16) & 0xff))
	buf.WriteByte(byte((in >> 8) & 0xff))
	buf.WriteByte(byte(in & 0xff))
	return buf.Bytes()
}

func GetSourceName(pc uintptr, file string, line int, ok bool) string {
	// in skip if we're using 1, so it will actually log the where the error happened, 0 = this function
	return file[strings.LastIndex(file, "/")+1:]
}

func GetSourceLineNum(pc uintptr, file string, line int, ok bool) int {
	// in skip if we're using 1, so it will actually log the where the error happened, 0 = this function
	return line
}

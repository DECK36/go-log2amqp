/*
log2amqp

A simple daemon that reads a file (tail -f style)
and sends every line to an AMQP exchange.

Intended for nginx access logs -- so it does some special
character encoding/escaping for that format.

TODO: decode Apache escaping

2014, DECK36 GmbH & Co. KG, <martin.schuette@deck36.de>
*/
package main

import (
	"flag"
	"fmt"
	"github.com/ActiveState/tail"
	"github.com/streadway/amqp"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// indicate what variables are our payload data,
// to improve readability (hopefully)
type Logline string

// all command line options
type CommandLineOptions struct {
	filename     *string
	uri          *string
	exchangeName *string
	exchangeType *string
	routingKey   *string
	verbose      *bool
	nofollow     *bool
}

var options CommandLineOptions

func init() {
	// this does not look right...
	// I am looking for a pattern how to group command line arguments in a struct
	options = CommandLineOptions{
		flag.String("file", "/var/log/syslog", "filename to watch"),
		flag.String("uri", "amqp://user:password@broker.example.com:5672/vhost", "AMQP URI"),
		flag.String("exchange", "logtest", "Durable AMQP exchange name"),
		flag.String("exchange-type", "fanout", "Exchange type - direct|fanout|topic|x-custom"),
		flag.String("key", "nginxlog", "AMQP routing key"),
		flag.Bool("v", false, "Verbose output"),
		flag.Bool("n", false, "Quit after file is read, do not wait for more data, do not read/write state"),
	}
	flag.Parse()
}

func readFileInode(fname string) uint64 {
	var stat syscall.Stat_t

	err := syscall.Stat(fname, &stat)
	if err != nil {
		return 0
	} else {
		return stat.Ino
	}
}

// readStateFile gets previously saved file stat, i.e. inode and offset
func readStateFile(fname string, statefile string, current_inode uint64) (offset int64) {
	var time int64
	var inode uint64
	offset = 0

	stateline, err := ioutil.ReadFile(statefile)
	if err != nil {
		return // no state
	}

	n, err := fmt.Sscanf(string(stateline), "Offset %d Time %d Inode %d\n",
		&offset, &time, &inode)
	if n != 3 || err != nil {
		log.Printf("ignoring statefile, cannot parse data in %s: %v", statefile, err)
		return
	}

	if current_inode != inode {
		log.Printf("not resuming file %s, changed inode from %d to %d\n",
			fname, inode, current_inode)
		return
	}

	log.Printf("resume logfile tail of file %s (inode %d) at offset %d\n",
		fname, inode, offset)
	return offset
}

// write inode and offset to continue later
func writeStateFile(statefile string, inode uint64, offset int64) {
	data := []byte(fmt.Sprintf("Offset %d Time %d Inode %d\n",
		offset, time.Now().UTC().Unix(), inode))
	ioutil.WriteFile(statefile, data, 0664)
}

// readLogsFromFile reads log lines from file and send them to `queue`
// notify `shutdown` when file is completely read
func readLogsFromFile(fname string, queue chan<- Logline, shutdown chan<- string, savestate <-chan bool) {
	var statefile string
	var offset int64
	var inode uint64
	var doFollowFile bool = !*options.nofollow

	if *options.verbose {
		log.Printf("readLogsFromFile: dofollow=%v", doFollowFile)
	}

	if doFollowFile {
		statefile = fname + ".state"
		inode     = readFileInode(fname)
		offset    = readStateFile(fname, statefile, inode)
	}

	// setup
	config := tail.Config{
		Follow:    doFollowFile,
		ReOpen:    doFollowFile,
		MustExist: true,
		Logger:    tail.DiscardingLogger,
		Location: &tail.SeekInfo{
			Offset: offset,
			Whence: 0,
		},
	}
	t, err := tail.TailFile(fname, config)
	if err != nil {
		shutdown <- fmt.Sprintf("cannot tail file %s: %v", fname, err)
	} else if *options.verbose {
		log.Printf("opened log file %s", fname)
	}

	// now just sleep and wait for input and control channel
	for {
		select {
		case line := <-t.Lines:
			if line != nil {
				queue <- Logline(line.Text)
			} else {
				shutdown <- "Logfile closed"
				return
			}
		case <-savestate:
			offset, _ := t.Tell()
			if doFollowFile {
				writeStateFile(statefile, inode, offset)
			}
			if *options.verbose {
				log.Printf("reading %s, now at offset %d", fname, offset)
			}
		}
	}
}

// open AMQP channel
func openAmqpChannel(amqpURI string, exchange string, exchangeType string, routingKey string) (connection *amqp.Connection, channel *amqp.Channel, err error) {
	// this is the important part:
	if *options.verbose {
		log.Println("connecting to ", amqpURI, "...")
	}
	connection, err = amqp.Dial(amqpURI)
	if err != nil {
		return nil, nil, fmt.Errorf("AMQP Dial: %s", err)
	}
	channel, err = connection.Channel()
	if err != nil {
		return nil, nil, fmt.Errorf("AMQP Channel: %s", err)
	}

	// here we only ensure the AMQP exchange exists
	err = channel.ExchangeDeclare(
		exchange,     // name
		exchangeType, // type
		true,         // durable
		false,        // auto-deleted
		false,        // internal
		false,        // noWait
		nil,          // arguments
	)
	if err != nil {
		return nil, nil, fmt.Errorf("Exchange Declare: %v", err)
	}
	return
}

// general error "handler"
func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
	}
}

// read logs from `queue` and send by AMQP
// TODO: there is too little error handling. in case of problems we simply panic and quit
func writeLogsToAmqp(queue <-chan Logline, shutdown chan<- string) {
	connection, channel, err := openAmqpChannel(*options.uri,
		*options.exchangeName, *options.exchangeType, *options.routingKey)
	failOnError(err, "cannot open AMQP channel")
	if *options.verbose {
		log.Println("opened AMQP connection and checked exchange")
	}
	defer connection.Close()
	defer channel.Close()

	go func() {
		notification := channel.NotifyClose(make(chan *amqp.Error))
		n := <-notification
		shutdown <- fmt.Sprintf("AMQP server closed connection: %v", n)
	}()

	for message := range queue {
		err := publishSingleMessageToAmqp(message, channel)
		if err != nil {
			failOnError(err, "AMQP error")
		} else if *options.verbose {
			fmt.Printf(".")
		}
	}
}

func publishSingleMessageToAmqp(message Logline, channel *amqp.Channel) error {
	// simple check of content type
	var contentType string
	if message[0] == '{' && message[len(message)-1] == '}' {
		contentType = "application/json"
	} else {
		contentType = "text/plain"
	}

	return channel.Publish(
		*options.exchangeName, // publish to an exchange
		*options.routingKey,   // routing to 0 or more queues
		false,                 // mandatory
		false,                 // immediate
		amqp.Publishing{
			Headers:         amqp.Table{},
			ContentType:     contentType,
			ContentEncoding: "",
			Body:            Unescape([]byte(message)),
			DeliveryMode:    amqp.Transient, // 1=non-persistent, 2=persistent
			Priority:        0,              // 0-9
			// a bunch of application/implementation-specific fields
		},
	)
}

// let the OS tell us to shutdown
func osSignalHandler(shutdown chan<- string) {
	var sigs = make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	sig := <-sigs  // this is the blocking part

	go func(){
		time.Sleep(2*time.Second)
		log.Fatalf("shutdown was ignored, bailing out now.\n")
	}()

	shutdown <- fmt.Sprintf("received signal %v", sig)
}

func main() {
	if *options.verbose {
		log.Printf("Start, verbose: %v, nofollow: %v", *options.verbose, *options.nofollow)
	}
	// let goroutines tell us to shutdown (on error)
	var sig_shutdown  = make(chan string)
	var file_shutdown = make(chan string)
	var amqp_shutdown = make(chan string)
	// the main data queue, between reader and writer goroutines
	var queue = make(chan Logline)

	// let the OS tell us to shutdown
	go osSignalHandler(sig_shutdown)

	// tell goroutine to save state before shutdown
	var savestate = make(chan bool)
	go readLogsFromFile(*options.filename, queue, file_shutdown, savestate)

	go writeLogsToAmqp(queue, amqp_shutdown)

	// keep track of last offset
	ticker := time.NewTicker(time.Second * 2)
	go func() {
		for _ = range ticker.C {
			savestate <- true
		}
	}()

	select {
	case message := <-sig_shutdown:
		if *options.verbose {
			log.Println("sig_shutdown:", message)
		}
	case message := <-file_shutdown:
		if *options.verbose {
			log.Println("file_shutdown:", message)
		}
	case message := <-amqp_shutdown:
		if *options.verbose {
			log.Println("amqp_shutdown:", message)
		}
		savestate <- true  // file reader still alive
	}
	if *options.verbose {
		log.Println("The End.")
	}
}

package main

import (
	"bufio"
	"flag"
	"fmt"
	"sync"
	"time"
	// "os"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Config struct {
	tcp          bool
	udp          bool
	debug        bool
	ExporterPort string
	ExporterAddr string
	Processes    []string
	regex        *regexp.Regexp
}

var (
	config Config
)

var (
	recvQueue = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "sockstat_receive_queue_bytes",
			Help: "Bytes waiting in the receive queue of the process",
		},
		[]string{"process_name", "pid", "fd", "protocol"},
	)
	sentQueue = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "sockstat_sent_queue_bytes",
			Help: "Bytes waiting in the sent queue of the process",
		},
		[]string{"process_name", "pid", "fd", "protocol"},
	)
	retransmissionCounter = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "sockstat_retransmissions_total",
			Help: "Number of packets that have been retransmitted",
		},
		[]string{"process_name", "pid", "fd", "protocol"},
	)
)

func printManual() {
	fmt.Println(`
SockStat Exporter Usage Manual:

This program exports socket statistics for processes in Prometheus format. 
You can specify processes, monitor TCP/UDP sockets, and run in debug mode.

Command-line Flags:
  -debug        Enable debug logging to stdout (default: false)
  -man          Print this manual and exit
  -t            Export TCP socket statistics (default: true)
  -u            Export UDP socket statistics (default: false)
  -config       Path to configuration file (optional)
  -addr         Expose on all network interfaces (default: localhost only)
  -port         Listening port for Prometheus metrics (default: 9258)
  -processes    Comma-separated list of process names to track(default tracks all processes)

Example Usage:
To run the exporter on port 9260, running in debug mode capturing both tcp and udp packets
  ./ss-exporter -debug -port=9260 -t -u -processes=myapp,anotherapp

Endpoints:
  /metrics      Exposes socket metrics in Prometheus format

Configuration:( NOT SUPPORTED RIGHT NOW)
If -config is provided, it must be a valid YAML file containing:
  - ExporterAddr: IP address to bind (e.g., "127.0.0.1")
  - ExporterPort: Port to listen on (e.g., "9258")
  - Processes:    List of processes to monitor

`)
	return
}

func logRequest(r *http.Request) {
	timestamp := time.Now().Format(time.RFC3339)
	clientIP := r.RemoteAddr
	requestedURI := r.RequestURI
	log.Printf("[%s] Client IP: %s Requested URI: %s", timestamp, clientIP, requestedURI)
}

func parseSSOutput(protocol string) {
	ssCommand := fmt.Sprintf("ss -op%s | sed 1d", protocol)
	if protocol == "t" {
		protocol = "tcp"
	} else {
		// log.Printf("GOT UDP SOCKET")
		protocol = "udp"
	}
	if config.debug {
		log.Printf("running %s:", ssCommand)
	}
	cmd := exec.Command("sh", "-c", ssCommand)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("error getting ss output for proceess: %v", err)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("error starting ss command: %v", err)
	}

	scanner := bufio.NewScanner(stdout)

	for scanner.Scan() {
		line := scanner.Text()
		if config.debug {
			log.Println(line)
		}

		parts := strings.Fields(line)
		length := len(parts)
		var users_field int
		processName, pid, fd, retrans := "", "", "", ""
		rq, sq := "", ""
		if protocol == "udp" {
			if length <= 4 {
				if config.debug {
					log.Printf("no associated process found, skipping")
				}
				continue
			} else {
				rq, sq = parts[0], parts[1]
				users_field = 4
			}
		} else {
			if length <= 5 {
				if config.debug {
					log.Printf("no associated process found, skipping")
				}
				continue
			} else {
				users_field = 5
				rq, sq = parts[1], parts[2]
				if length == 7 {
					temp := strings.Split(parts[6], ",")[2]
					retrans = temp[:len(temp)-1]
				}
			}
		}
		matches := config.regex.FindStringSubmatch(parts[users_field])
		// log.Printf("matches: %s, protocol: %s", matches, protocol)
		if config.debug {
			log.Printf("printing matches : %s", matches)
		}
		if len(matches) > 0 {
			processName = matches[1]
			pid = matches[2]
			fd = matches[3]
		} else {
			if config.debug {
				log.Printf("No matches found ,skipping")
			}
			continue
		}
		if config.debug {
			log.Printf("Process Name: %s ; pid: %s ; fd: %s ; retrans: %s", processName, pid, fd, retrans)
		}
		recvQueue.WithLabelValues(processName, pid, fd, protocol).Set(stringToFloat(rq))
		sentQueue.WithLabelValues(processName, pid, fd, protocol).Set(stringToFloat(sq))
		if retrans != "" {
			retransmissionCounter.WithLabelValues(processName, pid, fd, protocol).Set(stringToFloat(retrans))
		}
	}

	if err := cmd.Wait(); err != nil {
		log.Printf("Error waiting for ss command: %v", err)
	}
}

func parseProcessSSOutput(protocol string, processName string) {
	ssCommand := fmt.Sprintf("ss -op%s | grep %s", protocol, processName)
	if protocol == "t" {
		protocol = "tcp"
	} else {
		protocol = "udp"
	}
	if config.debug {
		log.Printf("running %s:", ssCommand)
	}
	cmd := exec.Command("sh", "-c", ssCommand)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("error getting ss output for process %s: %v", processName, err)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("Error starting ss command for process %s: %v", processName, err)
		return
	}

	scanner := bufio.NewScanner(stdout)

	if config.debug {
		log.Printf("Processing ss output for process: %s", processName)
	}

	for scanner.Scan() {
		line := scanner.Text()
		if config.debug {
			log.Println(line)
		}
		parts := strings.Fields(line)
		rq, sq := parts[1], parts[2]
		matches := config.regex.FindStringSubmatch(parts[5])
		if config.debug {
			log.Printf("printing matches: %s", matches)
		}
		pid, fd, retrans := "", "", ""
		if len(matches) > 0 {
			pid = matches[1]
			fd = matches[2]
		}
		if len(parts) == 7 {
			temp := strings.Split(parts[6], ",")[2]
			retrans = temp[:len(temp)-1]
		}
		if config.debug {
			log.Printf("Process Name: %s ; pid: %s ; fd: %s ; retrans: %s", processName, pid, fd, retrans)
		}
		recvQueue.WithLabelValues(processName, pid, fd, protocol).Set(stringToFloat(rq))
		sentQueue.WithLabelValues(processName, pid, fd, protocol).Set(stringToFloat(sq))
		if retrans != "" {
			retransmissionCounter.WithLabelValues(processName, pid, fd, protocol).Set(stringToFloat(retrans))
		}
	}

	if err := cmd.Wait(); err != nil {
		log.Printf("Error waiting for ss command for process %s: %v", processName, err)
	}
}

func stringToFloat(s string) float64 {
	if config.debug {
		log.Printf("Converting String: %s to float", s)
	}
	value, err := strconv.ParseFloat(s, 64)
	if err != nil {
		log.Printf("Error converting string to float64: %v", err)
		return 0
	}
	return value
}

func updateMetrics(w http.ResponseWriter, r *http.Request) {
	if config.debug {
		log.Println("Fetching metrics")
	}
	if config.debug {
		logRequest(r)
	}
	recvQueue.Reset()
	sentQueue.Reset()
	retransmissionCounter.Reset()
	var wg sync.WaitGroup
	if config.Processes[0] == "*" {
		if config.tcp {
			wg.Add(1)
			go func() {
				defer wg.Done()
				parseSSOutput("t")
			}()
			if config.debug {
				log.Printf("started tcp goroutine for all processes")
			}
		}
		if config.udp {
			wg.Add(1)
			go func() {
				defer wg.Done()
				parseSSOutput("u")
			}()
			if config.debug {
				log.Printf("started udp goroutine for all processes")
			}
		}
	} else {
		if config.udp {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for _, processName := range config.Processes {
					parseProcessSSOutput("u", processName)
				}
			}()
			if config.debug {
				log.Printf("started udp goroutine for %s processes", config.Processes)
			}
		}
		if config.tcp {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for _, processName := range config.Processes {
					parseProcessSSOutput("t", processName)
				}
			}()
			if config.debug {
				log.Printf("started tcp goroutine for %s processes", config.Processes)
			}
		}
	}
	wg.Wait()
	promhttp.Handler().ServeHTTP(w, r)
	if config.debug {
		log.Println("Metrics Fetched!")
	}
}

func main() {
	var (
		debug      = flag.Bool("debug", false, "print logs to stdout")
		man        = flag.Bool("man", false, "print manual")
		tcp        = flag.Bool("t", true, "export tcp sockets")
		udp        = flag.Bool("u", false, "export udp sockets")
		configPath = flag.String("config", "", "Path to config file")
		listenAddr = flag.Bool("addr", false, "Expose on all interfaces. Default is only localhost")
		listenPort = flag.String("port", "9258",
			"Address on which to expose metrics and web interface.")
		processes = flag.String("processes", "*", "Processes to track given as comma separated values. By default tracks all processes with open sockets")
	)
	flag.Parse()

	if *man {
		printManual()
		return
	}

	var trackProcesses []string
	if *processes != "*" {
		trackProcesses = strings.Split(*processes, ",")
		config.regex = regexp.MustCompile(`pid=(\d+),fd=(\d+)\)`)
	} else {
		trackProcesses = []string{"*"}
		// config.regex = regexp.MustCompile(`users:\(\("(.*)",pid=(\d+),fd=(\d+)\)`)
		config.regex = regexp.MustCompile(`users:\(\("([^"]+)",pid=(\d+),fd=(\d+)\)\)`)
	}

	// make config file
	if *configPath == "" {
		config.ExporterPort = *listenPort
		if *listenAddr {
			config.ExporterAddr = "0.0.0.0"
		} else {
			config.ExporterAddr = "127.0.0.1"
		}
		config.debug = *debug
		config.tcp = *tcp
		config.udp = *udp
		config.Processes = trackProcesses
	} //else{
	// 	//put config file parsing function here
	// }

	addr := config.ExporterAddr + ":" + config.ExporterPort
	if config.debug {
		log.Printf("displaying debug information\n")
		log.Printf("tracking the following processes: %s", trackProcesses)
		log.Printf("listening on: %s", addr)
	}
	prometheus.MustRegister(recvQueue)
	prometheus.MustRegister(sentQueue)
	prometheus.MustRegister(retransmissionCounter)

	http.HandleFunc("/metrics", updateMetrics)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
			<head><title>SockStat Exporter</title></head>
			<body>
			<h1>SockStat Exporter</h1>
			<p><a href="` + "/metrics" + `">Metrics</a></p>
			</body>
			</html>`))
	})
	log.Printf("Server started at %s/metrics", addr)
	log.Fatal(http.ListenAndServe(addr, nil))

}

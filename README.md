# ss_exporter

This program exports socket statistics for processes in Prometheus format.
You can specify processes, monitor TCP/UDP sockets, and run in debug mode.

Command-line Flags:
-debug Enable debug logging to stdout (default: false)
-man Print this manual and exit
-t Export TCP socket statistics (default: true)
-u Export UDP socket statistics (default: false)
-config Path to configuration file (optional)
-addr Expose on all network interfaces (default: localhost only)
-port Listening port for Prometheus metrics (default: 9258)
-processes Comma-separated list of process names to track(default tracks all processes)

Example Usage:
To run the exporter on port 9260, running in debug mode capturing both tcp and udp packets:
`./sockstat_exporter -debug -port=9260 -t -u -processes=<process_name>,<process_name>`

Endpoints:
/metrics Exposes socket metrics in Prometheus format

Configuration:( NOT SUPPORTED RIGHT NOW)
If -config is provided, it must be a valid YAML file containing:

- ExporterAddr: IP address to bind (e.g., "127.0.0.1")
- ExporterPort: Port to listen on (e.g., "9258")
- Processes: List of processes to monitor

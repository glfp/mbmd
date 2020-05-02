package server

import (
	"log"
	"sync"
	"time"

	influxdb "github.com/influxdata/influxdb1-client/v2"
)

const (
	writeTimeout = 30 * time.Second
)

// Influx is a influx publisher
type Influx struct {
	sync.Mutex
	client      influxdb.Client
	points      []*influxdb.Point
	pointsConf  influxdb.BatchPointsConfig
	interval    time.Duration
	measurement string
	verbose     bool
}

// NewInfluxClient creates new publisher for influx
func NewInfluxClient(
	url string,
	database string,
	measurement string,
	precision string,
	interval time.Duration,
	user string,
	password string,
	verbose bool,
) *Influx {
	client, err := influxdb.NewHTTPClient(influxdb.HTTPConfig{
		Addr:     url,
		Username: user,
		Password: password,
		Timeout:  writeTimeout,
	})
	if err != nil {
		log.Fatalf("influx: error creating client: %v", err)
	}

	if database == "" {
		log.Fatal("influx: missing database")
	}
	if measurement == "" {
		log.Fatal("influx: missing measurement")
	}

	// check connection
	go func(client influxdb.Client) {
		if _, _, err := client.Ping(writeTimeout); err != nil {
			log.Fatalf("influx: %s", err)
		}
	}(client)

	return &Influx{
		client: client,
		pointsConf: influxdb.BatchPointsConfig{
			Database:  database,
			Precision: precision,
		},
		interval:    interval,
		measurement: measurement,
		verbose:     verbose,
	}
}

// writeBatchPoints asynchronously writes the collected points to influx
func (m *Influx) writeBatchPoints() {
	m.Lock()

	// get current batch
	if len(m.points) == 0 {
		m.Unlock()
		return
	}

	// create new batch
	batch, err := influxdb.NewBatchPoints(m.pointsConf)
	if err != nil {
		log.Printf("influx: error creating batch: %v", err)
		m.Unlock()
		return
	}

	// replace current batch
	points := m.points
	m.points = nil
	m.Unlock()

	// write batch
	batch.AddPoints(points)
	if err := m.client.Write(batch); err != nil {
		log.Printf("influx: failed writing %d points, will retry: %v", len(points), err)

		// put points back at beginning of next batch
		m.Lock()
		m.points = append(points, m.points...)
		m.Unlock()
	}
}

// asyncWriter periodically calls writeBatchPoints
func (m *Influx) asyncWriter(exit <-chan bool) <-chan bool {
	done := make(chan bool) // signal writer stopped

	// async batch writer
	go func() {
		ticker := time.NewTicker(m.interval)
		for {
			select {
			case <-ticker.C:
				m.writeBatchPoints()
			case <-exit:
				ticker.Stop()
				m.writeBatchPoints()
				done <- true
				return
			}
		}
	}()

	return done
}

// Run Influx publisher
func (m *Influx) Run(in <-chan QuerySnip) {
	// run async writer
	exit := make(chan bool)     // exit signals to stop writer
	done := m.asyncWriter(exit) // done signals writer stopped

	for snip := range in {
		p, err := influxdb.NewPoint(
			m.measurement,
			map[string]string{
				"device": getDevice(snip.Device),
				"type":   snip.Measurement.String(),
			},
			map[string]interface{}{"value": getValue(snip.Device, snip.Measurement.String(), snip.Value)},
			snip.Timestamp,
		)
		if err != nil {
			log.Printf("influx: error creating point: %v", err)
			continue
		}
		//log.Printf("Influx new Point: Device %s, Type: %s, Value: %.3f", getDevice(snip.Device), snip.Measurement.String(), getValue(snip.Device, snip.Measurement.String(), snip.Value))
		
		m.Lock()
		m.points = append(m.points, p)
		m.Unlock()
	}

	// close write loop
	exit <- true
	<-done

	m.client.Close()
}

func getDevice(device string ) string {
	switch device {
		case "SDM2301.1":
			return "SDM1.1"		
		case "SDM2301.2":
			return "SDM1.2"
	}
	return device	
}

func getValue(device string, valueType string, value float64 ) float64 {
	switch device {
		case "SDM2301.2":
			switch valueType {
				case "Import":
					return value + 1000;
			}
	}
	return value	
}
package server

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/tpbowden/swarm-ingress-router/cache"
	"github.com/tpbowden/swarm-ingress-router/router"
	"github.com/tpbowden/swarm-ingress-router/service"
)

// Startable is anything which can be started and will block until stopped
type Startable interface {
	Start()
}

// Server holds all state for routing to services
type Server struct {
	bindAddress string
	cache       cache.Cache
	router      *router.Router
}

func (s *Server) syncServices() {
	var services []service.Service
	servicesJSON, getErr := s.cache.Get("services")

	if getErr != nil {
		log.Printf("Failed to load servics from cache: %v", getErr)
		return
	}

	err := json.Unmarshal(servicesJSON, &services)

	if err != nil {
		log.Print("Failed to sync services", err)
		return
	}

	s.router.UpdateTable(services)
	log.Printf("Routes updated")
}

// ServerHTTP is the default HTTP handler for services
func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	dnsName := strings.Split(req.Host, ":")[0]
	log.Printf("Started %s \"%s\" for %s using host %s", req.Method, req.URL, req.RemoteAddr, dnsName)
	secure := req.TLS != nil

	handler, ok := s.router.RouteToService(dnsName, secure)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "Failed to look up service")
		return
	}

	handler.ServeHTTP(w, req)
}

func (s *Server) getCertificate(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert, ok := s.router.CertificateForService(clientHello.ServerName)
	if !ok {
		return cert, errors.New("Failed to lookup certificate")
	}

	return cert, nil
}

func (s *Server) startHTTPServer() {
	bind := fmt.Sprintf("%s:8080", s.bindAddress)
	log.Printf("Server listening for HTTP on http://%s", bind)
	http.ListenAndServe(bind, s)
}

func (s *Server) startHTTPSServer() {
	bind := fmt.Sprintf("%s:8443", s.bindAddress)
	config := &tls.Config{GetCertificate: s.getCertificate}
	listener, _ := tls.Listen("tcp", bind, config)
	tlsServer := http.Server{Handler: s}

	log.Printf("Server listening for HTTPS on https://%s", bind)
	tlsServer.Serve(listener)
}

// Start start the server and listens for changes to the services
func (s *Server) Start() {
	go func() {
		s.syncServices()
		for {
			err := s.cache.Subscribe("inress-router", s.syncServices)
			log.Printf("Subscription to updates lost, retrying in 10 seconds: %v", err)
			time.Sleep(10 * time.Second)
		}
	}()
	go s.startHTTPServer()
	go s.startHTTPSServer()
	select {}
}

// NewServer returns a new instrance of the server
func NewServer(bind, redis string) Startable {
	router := router.NewRouter()
	cache := cache.NewCache(redis)
	return Startable(&Server{bindAddress: bind, router: router, cache: cache})
}

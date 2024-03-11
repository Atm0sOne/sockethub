package socket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Event - websocket server event
type Event struct {
	ctx        context.Context
	RemoteAddr string
	Channel    string
	Payload    []byte
}

// Context - returns empty event parent context
func (e *Event) Context() context.Context {
	return e.ctx
}

// Meta - defines websocket message meta info
type Meta struct {
	Channel string    `json:"channel"`
	Time    time.Time `json:"time"`
}

// Connection - defines websocket connection structure
type Connection struct {
	sync.Mutex
	ID   string
	Conn *websocket.Conn
}

type response struct {
	Meta    `json:"meta"`
	Payload any `json:"payload"`
}

func (c *Connection) Write(e *Event, m any) error {
	c.Lock()
	defer c.Unlock()

	r := response{
		Meta: Meta{
			Channel: e.Channel,
			Time:    time.Now(),
		},
		Payload: m,
	}

	return c.Conn.WriteJSON(r)
}

// Route - defines websocket server route
type route struct {
	Channel string
	Handler func(*Event) (any, error)
}

// Router - defines websocket server router
type Router struct {
	Routes map[string]*route
}

// Hub - websocket server connections hub
type hub struct {
	sync.RWMutex
	Store map[string]*Connection
}

// Server - websocket server
type Server struct {
	Upgrader websocket.Upgrader
	hub      *hub
	router   *Router
}

// Allowed - thrust websocket origins
type Allowed []string

// New - websocket server constructor
func New(allowed Allowed) *Server {

	upgrader := websocket.Upgrader{
		HandshakeTimeout: 5 * time.Second,
		ReadBufferSize:   1024,
		WriteBufferSize:  1024,
	}

	upgrader.CheckOrigin = func(r *http.Request) bool {
		for _, origin := range allowed {
			if strings.Contains(r.RemoteAddr, origin) {
				return true
			}
		}
		return false
	}

	return &Server{
		Upgrader: upgrader,
		hub: &hub{
			Store: make(map[string]*Connection),
		},
		router: &Router{
			Routes: make(map[string]*route),
		},
	}
}

// Handle - register new websocket channel handler function
func (s *Server) Handle(channel string, handler func(*Event) (any, error)) {
	s.router.Routes[channel] = &route{
		Channel: channel,
		Handler: handler,
	}
}

// Open - register new websocket connection in hub
func (s *Server) Open(w http.ResponseWriter, r *http.Request) {
	s.hub.Lock()
	defer s.hub.Unlock()

	conn, err := s.Upgrader.Upgrade(w, r, nil)

	if err != nil {
		log.Println(err)
		return
	}

	c := &Connection{
		ID:   r.RemoteAddr,
		Conn: conn,
	}

	s.hub.Store[c.ID] = c

	go s.listen(c)
	fmt.Printf("WebSocket")
	fmt.Printf("\tConnection: %s", c.ID)
	fmt.Printf("\tStatus: opened")
	fmt.Printf("\tTotal: %d", len(s.hub.Store))
	fmt.Printf("\tGoroutines: %d\n", runtime.NumGoroutine())
}

func (s *Server) route(e *Event) (*route, error) {
	var m struct {
		Meta Meta `json:"meta"`
	}

	if err := json.Unmarshal(e.Payload, &m); err != nil {
		return nil, err
	}

	if m.Meta.Channel != "" {
		e.Channel = m.Meta.Channel
		if route, ok := s.router.Routes[m.Meta.Channel]; ok {
			return route, nil
		}
	}

	return nil, errors.New("Unregistered channel request")
}

func (s *Server) handle(r *route, e *Event) (any, error) {
	e.ctx = context.Background()
	return r.Handler(e)
}

// Close - closes connection by id (removes connection from hub)
func (s *Server) Close(c *Connection) error {
	s.hub.Lock()
	defer s.hub.Unlock()

	normal := websocket.FormatCloseMessage(
		websocket.CloseNormalClosure,
		"Connection closed by server side")

	if err := c.Conn.WriteControl(
		websocket.CloseMessage,
		normal,
		time.Now().Add(time.Second)); err != nil {
		fmt.Println(err)
	}

	if err := c.Conn.Close(); err != nil {
		return err
	}

	delete(s.hub.Store, c.ID)
	return nil
}

// listen - event listener loop
func (s *Server) listen(c *Connection) {
	defer c.Conn.Close()

	for {
		_, p, err := c.Conn.ReadMessage()
		if err != nil {
			fmt.Printf("Websocket")
			fmt.Printf("\tConnection: %s", c.ID)
			fmt.Printf("\tError: %s\n", err.Error())
			if err := s.Close(c); err != nil {
				log.Println(err)
			}
			break
		}

		e := &Event{
			RemoteAddr: c.ID,
			Payload:    p,
		}

		r, err := s.route(e)

		if err != nil {
			fmt.Println(err)
			return
		}

		m, err := s.handle(r, e)

		if err != nil {
			fmt.Println(err)
			return
		}

		if m == nil {
			fmt.Println("Empty message")
		} else {
			if err := c.Write(e, m); err != nil {
				fmt.Println(err)
				if err := s.Close(c); err != nil {
					fmt.Println(err)
					break
				}
			}
		}
	}
}

// Shutdown - closes all active websocket connections and resets server hub
func (s *Server) Shutdown() {
	if len(s.hub.Store) > 0 {
		for _, c := range s.hub.Store {
			s.Close(c)
		}
	}
}

// Broadcast - writes message to opened websocket connections from hub
func (s *Server) Broadcast(e *Event, message any) error {
	s.hub.Lock()
	defer s.hub.Unlock()

	if len(s.hub.Store) > 0 {
		for _, c := range s.hub.Store {
			if err := c.Write(e, message); err != nil {
				fmt.Println(err)
				return err
			}
		}
	}

	return nil
}

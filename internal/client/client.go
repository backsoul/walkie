package client

import (
	"bytes"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/backsoul/walkie/internal/speech"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Permitir todas las solicitudes CORS
	},
}

var audioClients = make(map[*websocket.Conn]bool)
var speechClients = make(map[*websocket.Conn]bool)
var mu sync.Mutex

// WebSocket para enviar audio en tiempo real
func HandleAudioConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Error al actualizar la conexión: %v", err)
		return
	}
	defer ws.Close()

	mu.Lock()
	audioClients[ws] = true
	mu.Unlock()

	log.Println("Nuevo cliente conectado para envío de audio")

	for {
		_, audioData, err := ws.ReadMessage()
		if err != nil {
			log.Printf("Error al leer mensaje de audio: %v", err)
			break
		}

		// Transmitir el audio a todos los demás clientes conectados
		go func(audio []byte) {
			mu.Lock()
			defer mu.Unlock()
			for client := range audioClients {
				if client != ws {
					err := client.WriteMessage(websocket.BinaryMessage, audio)
					if err != nil {
						log.Printf("Error al retransmitir audio: %v", err)
						client.Close()
						delete(audioClients, client)
					}
				}
			}
		}(audioData)

		// Enviar el audio al WebSocket de procesamiento para transcripción
		go func(audio []byte) {
			mu.Lock()
			defer mu.Unlock()
			for client := range speechClients {
				err := client.WriteMessage(websocket.BinaryMessage, audio)
				if err != nil {
					log.Printf("Error al enviar audio para transcripción: %v", err)
					client.Close()
					delete(speechClients, client)
				}
			}
		}(audioData)

		// Dormir un breve momento para evitar sobrecargar el sistema
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	delete(audioClients, ws)
	mu.Unlock()

	log.Println("Cliente desconectado del envío de audio")
}

func HandleSpeechProcessing(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Error al actualizar la conexión: %v", err)
		return
	}
	defer ws.Close()

	mu.Lock()
	speechClients[ws] = true
	mu.Unlock()

	log.Println("Nuevo cliente conectado para procesamiento de audio a texto")

	var audioBuffer []byte
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	go func() {
		for {
			_, audioData, err := ws.ReadMessage()
			if err != nil {
				log.Printf("Error al recibir audio para transcripción: %v", err)
				break
			}
			// Acumula el audio a medida que lo recibe
			audioBuffer = append(audioBuffer, audioData...)
		}
	}()

	for {
		select {
		case <-ticker.C:
			// Procesar el buffer de audio cada 3 segundos
			go processAudioFragment(ws, &audioBuffer)
		}
	}
}

func processAudioFragment(ws *websocket.Conn, audioBuffer *[]byte) {
	// Copia el contenido del buffer actual
	audioData := make([]byte, len(*audioBuffer))
	copy(audioData, *audioBuffer)

	// Limpiar el buffer original para los próximos datos
	*audioBuffer = nil

	if len(audioData) == 0 {
		log.Println("No hay audio suficiente para procesar")
		return
	}

	// Enviar el audio acumulado al servicio de transcripción
	text, err := speech.StreamAudioToText(bytes.NewReader(audioData))
	if err != nil {
		log.Printf("Error al convertir audio a texto: %v", err)
		return
	}

	// Enviar el texto transcrito al cliente en formato de texto
	err = ws.WriteMessage(websocket.TextMessage, []byte(text))
	if err != nil {
		log.Printf("Error al enviar texto: %v", err)
	}
}

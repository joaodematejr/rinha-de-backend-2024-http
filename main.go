package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Cliente struct {
	ID     int `bson:"id"`
	Limite int `bson:"limite"`
	Saldo  int `bson:"saldo_inicial"`
}

type Transacao struct {
	Valor       int       `json:"valor"`
	Tipo        string    `json:"tipo"`
	Descricao   string    `json:"descricao"`
	RealizadaEm time.Time `json:"realizada_em"`
}

type RespostaTransacao struct {
	Limite int `json:"limite"`
	Saldo  int `json:"saldo"`
}

type Saldo struct {
	Total       int       `json:"total"`
	DataExtrato time.Time `json:"data_extrato"`
	Limite      int       `json:"limite"`
}

var (
	dbClient     *mongo.Client
	clienteMutex sync.Mutex
	errNotFound  = errors.New("Cliente não encontrado")
)

func connectToMongoDB(uri string, errCh chan<- error) {
	clientOptions := options.Client().
		SetTimeout(time.Second * 6).
		SetMaxPoolSize(350).
		SetMinPoolSize(150).
		ApplyURI(uri).
		SetConnectTimeout(10 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		errCh <- fmt.Errorf("failed to connect to MongoDB: %w", err)
		return
	}

	// Ensure the connection is established by pinging the MongoDB server
	err = client.Ping(ctx, nil)
	if err != nil {
		errCh <- fmt.Errorf("failed to ping MongoDB server: %w", err)
		return
	}

	// Set the global dbClient variable
	dbClient = client
}

func getPort() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return port
}

func criarTransacao(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	params := mux.Vars(r)
	clienteID, err := strconv.Atoi(params["id"])
	if err != nil {
		http.Error(w, "ID do cliente deve ser um número inteiro", http.StatusBadRequest)
		return
	}

	var transacao Transacao
	err = json.NewDecoder(r.Body).Decode(&transacao)
	if err != nil {
		http.Error(w, "Erro ao decodificar o corpo da requisição", http.StatusBadRequest)
		return
	}

	if transacao.Valor <= 0 {
		http.Error(w, "O valor da transação deve ser um número inteiro positivo", http.StatusUnprocessableEntity)
		return
	}

	if transacao.Tipo != "c" && transacao.Tipo != "d" {
		http.Error(w, "O tipo da transação deve ser 'c' para crédito ou 'd' para débito", http.StatusUnprocessableEntity)
		return
	}

	if len(transacao.Descricao) < 1 || len(transacao.Descricao) > 10 {
		http.Error(w, "A descrição da transação deve ter entre 1 e 10 caracteres", http.StatusUnprocessableEntity)
		return
	}

	cliente, err := buscarCliente(clienteID)
	if err != nil {
		if strings.Contains(err.Error(), "não encontrado") {
			http.Error(w, "Cliente não encontrado", http.StatusUnprocessableEntity)
		} else {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		}
		return
	}

	respostaCh := make(chan *RespostaTransacao)
	errCh := make(chan error)

	go func() {
		resposta, err := realizarTransacao(cliente, transacao)
		if err != nil {
			errCh <- err
			return
		}
		respostaCh <- resposta
	}()

	select {
	case resposta := <-respostaCh:
		json.NewEncoder(w).Encode(resposta)
	case err := <-errCh:
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	}
}

func getExtrato(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	params := mux.Vars(r)
	clienteID, err := strconv.Atoi(params["id"])
	if err != nil {
		http.Error(w, "ID do cliente deve ser um número inteiro", http.StatusBadRequest)
		return
	}

	clienteCh := make(chan *Cliente)
	transacoesCh := make(chan []Transacao)
	errCh := make(chan error)

	go func() {
		cliente, err := buscarCliente(clienteID)
		if err != nil {
			errCh <- err
			return
		}
		clienteCh <- cliente
	}()

	go func() {
		transacoes, err := getUltimasTransacoes(clienteID)
		if err != nil {
			errCh <- err
			return
		}
		transacoesCh <- transacoes
	}()

	var cliente *Cliente
	var transacoes []Transacao
	for i := 0; i < 2; i++ {
		select {
		case cliente = <-clienteCh:
		case trans := <-transacoesCh:
			transacoes = trans
		case err := <-errCh:
			if strings.Contains(err.Error(), "não encontrado") {
				http.Error(w, "Cliente não encontrado", http.StatusNotFound)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
	}

	resposta := struct {
		Saldo             Saldo       `json:"saldo"`
		UltimasTransacoes []Transacao `json:"ultimas_transacoes"`
	}{
		Saldo: Saldo{
			Total:       cliente.Saldo,
			DataExtrato: time.Now(),
			Limite:      cliente.Limite,
		},
		UltimasTransacoes: transacoes,
	}

	if err := json.NewEncoder(w).Encode(resposta); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func buscarCliente(id int) (*Cliente, error) {
	if dbClient == nil {
		return nil, errors.New("Cliente MongoDB não inicializado")
	}

	collection := dbClient.Database("rinha").Collection("clientes")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var cliente Cliente
	err := collection.FindOne(ctx, bson.M{"id": id}).Decode(&cliente)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, errNotFound
		} else if strings.Contains(err.Error(), "Premature close") {
			return nil, errors.New("Conexão fechada prematuramente ao buscar cliente")
		}
		return nil, err
	}

	return &cliente, nil
}

func realizarTransacao(cliente *Cliente, transacao Transacao) (*RespostaTransacao, error) {
	if dbClient == nil {
		return nil, errors.New("Cliente MongoDB não inicializado")
	}

	clienteMutex.Lock()
	defer clienteMutex.Unlock()

	var novoSaldo int
	switch transacao.Tipo {
	case "d":
		novoSaldo = cliente.Saldo - transacao.Valor
		if novoSaldo < -cliente.Limite {
			return nil, errors.New("Transação de débito deixaria o saldo abaixo do limite negativo")
		}
	case "c":
		novoSaldo = cliente.Saldo + transacao.Valor
	default:
		return nil, errors.New("Tipo de transação inválido")
	}

	novaTransacao := Transacao{
		Valor:       transacao.Valor,
		Tipo:        transacao.Tipo,
		Descricao:   transacao.Descricao,
		RealizadaEm: time.Now(),
	}

	collection := dbClient.Database("rinha").Collection("clientes")
	filter := bson.M{"id": cliente.ID}
	update := bson.M{
		"$set": bson.M{
			"saldo_inicial": novoSaldo,
		},
		"$push": bson.M{
			"historico_transacoes": novaTransacao,
		},
	}
	_, err := collection.UpdateOne(context.Background(), filter, update)
	if err != nil {
		return nil, fmt.Errorf("Erro ao atualizar saldo do cliente: %v", err)
	}

	return &RespostaTransacao{
		Limite: cliente.Limite,
		Saldo:  novoSaldo,
	}, nil
}

func getUltimasTransacoes(id int) ([]Transacao, error) {
	if dbClient == nil {
		return nil, errors.New("Cliente MongoDB não inicializado")
	}

	// Definir a coleção e o filtro para encontrar o cliente pelo ID
	collection := dbClient.Database("rinha").Collection("clientes")
	filtro := bson.M{"id": id}

	// Definir opções para a consulta, ordenando por data/hora em ordem decrescente e limitando a 10 transações
	opcoes := options.FindOne().SetSort(bson.D{{Key: "historico_transacoes.realizadaem", Value: -1}}).SetProjection(bson.M{"historico_transacoes": bson.M{"$slice": -10}})

	// Realizar a consulta
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Estrutura para armazenar o resultado da consulta
	var cliente struct {
		HistoricoTransacoes []Transacao `bson:"historico_transacoes"`
	}

	// Realizar a consulta e decodificar o resultado para a estrutura 'cliente'
	err := collection.FindOne(ctx, filtro, opcoes).Decode(&cliente)
	if err != nil {
		return nil, fmt.Errorf("Erro ao buscar o cliente: %v", err)
	}

	// Retornar o histórico de transações do cliente
	return cliente.HistoricoTransacoes, nil
}

func main() {
	fmt.Println("Iniciando servidor...")
	r := mux.NewRouter()

	// Connect to MongoDB
	dbErrCh := make(chan error, 1)

	// Connect to MongoDB concurrently
	go connectToMongoDB("mongodb://admin:admin@db:27017/rinha?socketTimeoutMS=360000&connectTimeoutMS=360000&maxPoolSize=10&minPoolSize=5&waitQueueMultiple=10&waitQueueTimeoutMS=360000&readPreference=primary&writeConcern=majority&readConcern=majority", dbErrCh)

	// Define HTTP routes
	r.HandleFunc("/clientes/{id}/transacoes", criarTransacao).Methods("POST")
	r.HandleFunc("/clientes/{id}/extrato", getExtrato).Methods("GET")

	// Start HTTP server
	server := &http.Server{
		Addr:    ":" + getPort(),
		Handler: r,
	}

	httpErrCh := make(chan error, 1)
	go func() {
		httpErrCh <- server.ListenAndServe()
	}()

	// Graceful Shutdown Handling
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	fmt.Println("Desligando servidor...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Erro ao desligar o servidor: %v\n", err)
	}

	select {
	case err := <-dbErrCh:
		if err != nil {
			log.Fatalf("Erro ao conectar ao banco de dados: %v\n", err)
		}
	case err := <-httpErrCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("Erro no servidor: %v\n", err)
		}
	}

	fmt.Println("Servidor desligado")
}

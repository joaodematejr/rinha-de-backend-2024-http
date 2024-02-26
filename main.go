package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
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
	ClienteID   int       `json:"cliente_id"`
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

var dbClient *mongo.Client
var clienteMutex sync.Mutex
var ErrClienteNaoEncontrado = errors.New("Cliente não encontrado")

func connectToMongoDB(uri string) (*mongo.Client, error) {
	clientOptions := options.Client().ApplyURI(uri)
	clientOptions = clientOptions.SetConnectTimeout(10 * time.Second)
	var client *mongo.Client
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		client, err = mongo.Connect(context.Background(), clientOptions)
		if err == nil {
			return client, nil
		}
		time.Sleep(2 * time.Second)
	}
	return nil, err
}

func main() {
	fmt.Println("Iniciando servidor...")
	r := mux.NewRouter()

	var err error
	dbClient, err = connectToMongoDB("mongodb://admin:admin@localhost:27017")
	if err != nil {
		log.Fatalf("Erro ao conectar ao banco de dados: %v\n", err)
	}
	defer dbClient.Disconnect(context.Background())

	r.HandleFunc("/clientes/{id}/transacoes", criarTransacao).Methods("POST")
	r.HandleFunc("/clientes/{id}/extrato", getExtrato).Methods("GET")

	fmt.Println("Servidor rodando na porta 8080...")
	log.Fatal(http.ListenAndServe(":8080", r))
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error)

	var cliente Cliente
	go func() {
		err := collection.FindOne(ctx, bson.M{"id": id}).Decode(&cliente)
		if err != nil {
			if err == mongo.ErrNoDocuments {
				errCh <- fmt.Errorf("Cliente com o ID %d não encontrado", id)
			} else {
				errCh <- err
			}
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		return nil, ctx.Err()
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

	collection := dbClient.Database("rinha").Collection("clientes")
	filter := bson.M{"id": cliente.ID}
	update := bson.M{"$set": bson.M{"saldo_inicial": novoSaldo}}
	_, err := collection.UpdateOne(context.Background(), filter, update)
	if err != nil {
		return nil, fmt.Errorf("Erro ao atualizar saldo do cliente: %v", err)
	}

	novaTransacao := Transacao{
		Valor:       transacao.Valor,
		Tipo:        transacao.Tipo,
		Descricao:   transacao.Descricao,
		RealizadaEm: time.Now(),
		ClienteID:   cliente.ID,
	}
	collectionHistorico := dbClient.Database("rinha").Collection("historico_transacoes")
	_, err = collectionHistorico.InsertOne(context.Background(), novaTransacao)
	if err != nil {
		return nil, fmt.Errorf("Erro ao registrar transação no histórico: %v", err)
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

	collection := dbClient.Database("rinha").Collection("historico_transacoes")
	filtro := bson.M{"clienteid": id}
	opcoes := options.Find().SetSort(bson.D{{Key: "realizada_em", Value: -1}}).SetLimit(10)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	semaphore := make(chan struct{}, 5)
	defer close(semaphore)

	resultChan := make(chan []Transacao)
	errChan := make(chan error)

	go func() {
		semaphore <- struct{}{}
		defer func() { <-semaphore }()

		cursor, err := collection.Find(ctx, filtro, opcoes)
		if err != nil {
			errChan <- err
			return
		}
		defer cursor.Close(ctx)

		var transacoes []Transacao
		for cursor.Next(ctx) {
			var transacao Transacao
			if err := cursor.Decode(&transacao); err != nil {
				errChan <- err
				return
			}
			transacoes = append(transacoes, transacao)
		}

		if err := cursor.Err(); err != nil {
			errChan <- err
			return
		}

		resultChan <- transacoes
	}()

	select {
	case transacoes := <-resultChan:
		return transacoes, nil
	case err := <-errChan:
		return nil, err
	case <-ctx.Done():
		<-semaphore
		return nil, ctx.Err()
	}
}

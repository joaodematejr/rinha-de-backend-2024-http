package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
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
	Total       int    `json:"total"`
	DataExtrato string `json:"data_extrato"`
	Limite      int    `json:"limite"`
}

var dbClient *mongo.Client

var ErrClienteNaoEncontrado = errors.New("Cliente não encontrado")

func main() {
	fmt.Println("Iniciando servidor...")
	r := mux.NewRouter()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var err error
	dbClient, err = mongo.Connect(ctx, options.Client().ApplyURI("mongodb://admin:admin@localhost:27017"))
	if err != nil {
		log.Fatalf("Erro ao conectar ao banco de dados: %v\n", err)
	}
	defer dbClient.Disconnect(ctx)

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
		http.Error(w, "O valor da transação deve ser um número inteiro positivo", http.StatusBadRequest)
		return
	}

	if transacao.Tipo != "c" && transacao.Tipo != "d" {
		http.Error(w, "O tipo da transação deve ser 'c' para crédito ou 'd' para débito", http.StatusBadRequest)
		return
	}

	if len(transacao.Descricao) < 1 || len(transacao.Descricao) > 10 {
		http.Error(w, "A descrição da transação deve ter entre 1 e 10 caracteres", http.StatusBadRequest)
		return
	}

	resposta, err := realizarTransacao(clienteID, transacao)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	json.NewEncoder(w).Encode(resposta)
}

func getExtrato(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	params := mux.Vars(r)
	clienteID, err := strconv.Atoi(params["id"])
	if err != nil {
		http.Error(w, "ID do cliente deve ser um número inteiro", http.StatusBadRequest)
		return
	}

	cliente, err := buscarCliente(clienteID)
	if err != nil {
		if err == ErrClienteNaoEncontrado {
			http.Error(w, "Cliente não encontrado", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	transacoes, err := getUltimasTransacoes(clienteID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resposta := struct {
		Saldo             Saldo       `json:"saldo"`
		UltimasTransacoes []Transacao `json:"ultimas_transacoes"`
	}{
		Saldo: Saldo{
			Total:       cliente.Saldo,
			DataExtrato: time.Now().UTC().Format("2006-01-02T15:04:05.999Z"),
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var cliente Cliente
	err := collection.FindOne(ctx, bson.M{"id": id}).Decode(&cliente)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, ErrClienteNaoEncontrado
		}
		return nil, err
	}
	return &cliente, nil
}

func realizarTransacao(id int, transacao Transacao) (*RespostaTransacao, error) {
	cliente, err := buscarCliente(id)
	if err != nil {
		return nil, err
	}

	if transacao.Valor < (-1 * cliente.Limite) {
		return nil, errors.New("Transação de débito deixaria o saldo inconsistente")
	}

	cliente.Saldo = transacao.Valor

	collection := dbClient.Database("rinha").Collection("clientes")
	filter := bson.M{"id": id}
	update := bson.M{"$set": bson.M{"saldo_inicial": cliente.Saldo}}
	_, err = collection.UpdateOne(context.Background(), filter, update)
	if err != nil {
		return nil, err
	}

	novaTransacao := Transacao{
		Valor:       transacao.Valor,
		Tipo:        transacao.Tipo,
		Descricao:   transacao.Descricao,
		RealizadaEm: time.Now(),
		ClienteID:   id,
	}

	collectionHistorico := dbClient.Database("rinha").Collection("historico_transacoes")
	_, err = collectionHistorico.InsertOne(context.Background(), novaTransacao)
	if err != nil {
		return nil, err
	}

	resposta := &RespostaTransacao{
		Limite: cliente.Limite,
		Saldo:  cliente.Saldo,
	}

	return resposta, nil
}

func getUltimasTransacoes(id int) ([]Transacao, error) {
	collection := dbClient.Database("rinha").Collection("historico_transacoes")
	filtro := bson.M{"clienteid": id}
	opcoes := options.Find().SetSort(bson.D{{"realizada_em", -1}}).SetLimit(10)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor, err := collection.Find(ctx, filtro, opcoes)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var transacoes []Transacao
	for cursor.Next(ctx) {
		var transacao Transacao
		if err := cursor.Decode(&transacao); err != nil {
			return nil, err
		}
		transacoes = append(transacoes, transacao)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return transacoes, nil
}

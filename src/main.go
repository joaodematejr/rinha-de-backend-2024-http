package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Cliente representa a estrutura de um cliente
type Cliente struct {
	ID     int     `json:"id" bson:"_id"`
	Limite int     `json:"limite" bson:"limit"`
	Saldo  float64 `json:"saldo" bson:"balance"`
}

// Transacao representa a estrutura de uma transação
type Transacao struct {
	Valor       int       `json:"valor"`
	Tipo        string    `json:"tipo"`
	Descricao   string    `json:"descricao"`
	RealizadaEm time.Time `json:"realizada_em"`
}

// Configuração do MongoDB
var mongoURI = "mongodb://admin:admin@localhost:27017"
var dbName = "rinha"
var collectionName = "clientes"

// Conexão com o MongoDB
var client *mongo.Client

// Inicializa a conexão com o MongoDB
func init() {
	// Conectar ao MongoDB
	clientOptions := options.Client().ApplyURI(mongoURI)
	var err error
	client, err = mongo.Connect(context.Background(), clientOptions)
	if err != nil {
		log.Fatal(err)
	}

	// Verificar conexão
	err = client.Ping(context.Background(), nil)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Conexão com o MongoDB estabelecida com sucesso")

	// Criar o índice único para evitar IDs duplicados
	indexModel := mongo.IndexModel{
		Keys:    bson.M{"_id": 10},
		Options: options.Index().SetUnique(true),
	}
	_, err = client.Database(dbName).Collection(collectionName).Indexes().CreateOne(context.Background(), indexModel)
	if err != nil {
		log.Fatal(err)
	}
}

// CriarTransacao cria uma nova transação para um cliente
func CriarTransacao(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	clienteID := params["id"]

	var cliente Cliente
	err := client.Database(dbName).Collection(collectionName).FindOne(context.Background(), bson.M{"_id": clienteID}).Decode(&cliente)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	var transacao Transacao
	err = json.NewDecoder(r.Body).Decode(&transacao)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if transacao.Tipo == "d" && float64(transacao.Valor) > cliente.Saldo+float64(cliente.Limite) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}

	if transacao.Tipo == "c" {
		cliente.Saldo += float64(transacao.Valor)
	} else {
		cliente.Saldo -= float64(transacao.Valor)
	}

	transacao.RealizadaEm = time.Now()
	clienteIDInt := parseInt(clienteID)
	_, err = client.Database(dbName).Collection(collectionName).UpdateOne(context.Background(), bson.M{"_id": clienteIDInt}, bson.M{"$push": bson.M{"transacoes": transacao}, "$set": bson.M{"saldo": cliente.Saldo}})
	if err != nil {
		log.Fatal(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(cliente)
}

// ObterExtrato retorna o extrato de transações de um cliente
func ObterExtrato(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	clienteID := params["id"]

	var cliente Cliente
	err := client.Database(dbName).Collection(collectionName).FindOne(context.Background(), bson.M{"_id": clienteID}).Decode(&cliente)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(cliente)
}

// Função auxiliar para converter uma string em int
func parseInt(str string) int {
	val, err := strconv.Atoi(str)
	if err != nil {
		return 0
	}
	return val
}

func main() {
	router := mux.NewRouter()

	router.HandleFunc("/clientes/{id}/transacoes", CriarTransacao).Methods("POST")
	router.HandleFunc("/clientes/{id}/extrato", ObterExtrato).Methods("GET")

	log.Fatal(http.ListenAndServe(":8080", router))
}

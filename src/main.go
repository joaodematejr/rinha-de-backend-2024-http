package main

import (
	"encoding/json"
	"github.com/gorilla/mux"
	"log"
	"net/http"
	"strconv"
	"time"
)

type TransactionRequest struct {
	Valor     int    `json:"valor"`
	Tipo      string `json:"tipo"`
	Descricao string `json:"descricao"`
}

type TransactionResponse struct {
	Limite int `json:"limite"`
	Saldo  int `json:"saldo"`
}

type Transaction struct {
	Valor       int       `json:"valor"`
	Tipo        string    `json:"tipo"`
	Descricao   string    `json:"descricao"`
	RealizadaEm time.Time `json:"realizada_em"`
}

type StatementResponse struct {
	Saldo             Balance       `json:"saldo"`
	UltimasTransacoes []Transaction `json:"ultimas_transacoes"`
}

type Balance struct {
	Total       int       `json:"total"`
	DataExtrato time.Time `json:"data_extrato"`
	Limite      int       `json:"limite"`
	Saldo       int       `json:"saldo"`
}

var clients = map[int]Balance{
	1: {100000, time.Now(), 0, 0},
	2: {80000, time.Now(), 0, 0},
	3: {1000000, time.Now(), 0, 0},
	4: {10000000, time.Now(), 0, 0},
	5: {500000, time.Now(), 0, 0},
}

func TransactionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	idInt, err := strconv.Atoi(id)
	var req TransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err != nil {

	}

	res := TransactionResponse{clients[idInt].Limite, clients[idInt].Saldo}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func StatementHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	idInt, err := strconv.Atoi(id)
	if err != nil {

	}

	res := TransactionResponse{clients[idInt].Limite, clients[idInt].Saldo}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func main() {
	r := mux.NewRouter()
	r.HandleFunc("/clientes/{id}/transacoes", TransactionHandler).Methods("POST")
	r.HandleFunc("/clientes/{id}/extrato", StatementHandler).Methods("GET")

	log.Fatal(http.ListenAndServe(":8080", r))
}

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Transacao struct {
	Valor     float64 `json:"valor"`
	Tipo      string  `json:"tipo"`
	Descricao string  `json:"descricao"`
}

func handleTransacao(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[len("/clientes/"):]
	var transacao Transacao
	err := json.NewDecoder(r.Body).Decode(&transacao)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, "Transação criada para o cliente %s: %+v\n", id, transacao)
}

func main() {
	http.HandleFunc("/clientes/", handleTransacao)
	fmt.Println("Servidor escutando na porta 8080...")
	http.ListenAndServe(":8080", nil)
}

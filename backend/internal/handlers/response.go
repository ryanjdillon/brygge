package handlers

import (
	"net/http"

	"github.com/brygge-klubb/brygge/internal/shared"
)

type errorResponse = shared.ErrorResponse

func JSON(w http.ResponseWriter, status int, data any) {
	shared.JSON(w, status, data)
}

func Error(w http.ResponseWriter, status int, message string) {
	shared.Error(w, status, message)
}


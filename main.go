package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/thuanvo2405/chirpyGo/internal/auth"
	"github.com/thuanvo2405/chirpyGo/internal/database"
)

type apiConfig struct {
	fileserverHits atomic.Int32
	database       *database.Queries
	platform       string
	jwtSecretKey   string
}

type User struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Email     string    `json:"email"`
	Token     string    `json:"token"`
}

type Chirp struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Body      string    `json:"body"`
	UserID    uuid.UUID `json:"user_id"`
}

func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (cfg *apiConfig) handlerMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf(`<html>
  <body>
    <h1>Welcome, Chirpy Admin</h1>
    <p>Chirpy has been visited %d times!</p>
  </body>
	</html>`, cfg.fileserverHits.Load())
	w.Write([]byte(msg))
}

func (cfg *apiConfig) handlerReset(w http.ResponseWriter, r *http.Request) {
	if cfg.platform != "dev" {
		respondWithError(w, http.StatusForbidden, "Forbidden")
		return
	}

	err := cfg.database.DeleteUsers(r.Context())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't delete users")
		return
	}

	cfg.fileserverHits.Store(0)
	respondWithJSON(w, http.StatusOK, struct {
		Hits int `json:"hits"`
	}{
		Hits: 0,
	})
}

func respondWithError(w http.ResponseWriter, code int, msg string) {
	type returnVals struct {
		Error string `json:"error"`
	}

	respondWithJSON(w, code, returnVals{
		Error: msg,
	})
}

func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	dat, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error marshalling JSON: %s", err)
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(dat)
}

func main() {
	godotenv.Load()
	dbURL := os.Getenv("DB_URL")
	JWT_SECRET_KEY := os.Getenv("JWT_SECRET_KEY")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("err: %v", err)
	}

	dbQueries := database.New(db)

	mux := http.NewServeMux()
	apiCfg := apiConfig{
		fileserverHits: atomic.Int32{},
		database:       dbQueries,
		platform:       os.Getenv("PLATFORM"),
		jwtSecretKey:   JWT_SECRET_KEY,
	}

	mux.Handle("/app/", apiCfg.middlewareMetricsInc(http.StripPrefix("/app", http.FileServer(http.Dir(".")))))
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("GET /admin/metrics", apiCfg.handlerMetrics)
	mux.HandleFunc("POST /admin/reset", apiCfg.handlerReset)

	mux.HandleFunc("POST /api/chirps", func(w http.ResponseWriter, r *http.Request) {
		type parameters struct {
			Body string `json:"body"`
		}

		token, err := auth.GetBearerToken(r.Header)
		if err != nil {
			respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT")
			return
		}

		userID, err := auth.ValidateJWT(token, apiCfg.jwtSecretKey)
		if err != nil {
			respondWithError(w, http.StatusUnauthorized, "Invalid token")
			return
		}

		decoder := json.NewDecoder(r.Body)
		params := parameters{}
		err = decoder.Decode(&params)
		if err != nil {
			respondWithError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		const maxChirpLength = 140
		if len(params.Body) > maxChirpLength {
			respondWithError(w, http.StatusBadRequest, "Chirp is too long")
			return
		}

		invalid_words := []string{"kerfuffle", "sharbert", "fornax"}
		words := strings.Split(params.Body, " ")
		for idx, word := range words {
			if slices.Contains(invalid_words, strings.ToLower(word)) {
				words[idx] = "****"
			}
		}

		newChirp, err := apiCfg.database.CreateChirp(r.Context(), database.CreateChirpParams{
			Body:   strings.Join(words, " "),
			UserID: userID,
		})
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "Couldn't create chirp")
			return
		}

		chirp := Chirp{
			ID:        newChirp.ID,
			CreatedAt: newChirp.CreatedAt,
			UpdatedAt: newChirp.UpdatedAt,
			Body:      newChirp.Body,
			UserID:    newChirp.UserID,
		}

		respondWithJSON(w, http.StatusCreated, chirp)
	})

	mux.HandleFunc("POST /api/users", func(w http.ResponseWriter, r *http.Request) {
		type paramaters struct {
			Password string `json:"password"`
			Email    string `json:"email"`
		}

		decoder := json.NewDecoder(r.Body)
		params := paramaters{}
		err := decoder.Decode(&params)
		if err != nil {
			respondWithError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		hashedPassword, err := auth.HashPassword(params.Password)
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "Couldn't hash password")
			return
		}

		newUser, err := apiCfg.database.CreateUser(r.Context(), database.CreateUserParams{
			Email:          params.Email,
			HashedPassword: hashedPassword,
		})
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "Couldn't create user")
			return
		}

		user := User{
			ID:        newUser.ID,
			CreatedAt: newUser.CreatedAt,
			UpdatedAt: newUser.UpdatedAt,
			Email:     newUser.Email,
		}

		respondWithJSON(w, http.StatusCreated, user)
	})

	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		type paramaters struct {
			Password         string `json:"password"`
			Email            string `json:"email"`
			ExpiresInSeconds int    `json:"expires_in_seconds"`
		}

		decoder := json.NewDecoder(r.Body)
		params := paramaters{}
		err := decoder.Decode(&params)
		if err != nil {
			respondWithError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		defaultExpiration := time.Hour
		if params.ExpiresInSeconds > 0 && params.ExpiresInSeconds < 3600 {
			defaultExpiration = time.Duration(params.ExpiresInSeconds) * time.Second
		}

		userDB, err := apiCfg.database.GetUserByEmail(r.Context(), params.Email)
		if err != nil {
			respondWithError(w, http.StatusUnauthorized, "Incorrect email or password")
			return
		}

		checkHash, err := auth.CheckPasswordHash(params.Password, userDB.HashedPassword)
		if err != nil {
			respondWithError(w, http.StatusUnauthorized, "Incorrect email or password")
			return
		}

		if !checkHash {
			respondWithError(w, http.StatusUnauthorized, "Incorrect email or password")
			return
		}

		token, err := auth.MakeJWT(userDB.ID, apiCfg.jwtSecretKey, defaultExpiration)
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, err.Error())
			return
		}

		user := User{
			ID:        userDB.ID,
			CreatedAt: userDB.CreatedAt,
			UpdatedAt: userDB.UpdatedAt,
			Email:     userDB.Email,
			Token:     token,
		}

		respondWithJSON(w, http.StatusOK, user)
	})

	mux.HandleFunc("GET /api/chirps", func(w http.ResponseWriter, r *http.Request) {
		chirpsDB, err := apiCfg.database.GetAllChirps(r.Context())
		if err != nil {
			log.Fatalf("Can't get chirps: %v", err)
		}

		chirps := []Chirp{}
		for _, dbChirp := range chirpsDB {
			chirps = append(chirps, Chirp{
				ID:        dbChirp.ID,
				CreatedAt: dbChirp.CreatedAt,
				UpdatedAt: dbChirp.UpdatedAt,
				Body:      dbChirp.Body,
				UserID:    dbChirp.UserID,
			})
		}

		respondWithJSON(w, http.StatusOK, chirps)
	})

	mux.HandleFunc("GET /api/chirps/{chirpID}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("chirpID")

		uuid, err := uuid.Parse(id)
		if err != nil {
			respondWithError(w, http.StatusBadRequest, "Invalid chirp ID")
			return
		}

		chirpDB, err := apiCfg.database.GetChirpById(r.Context(), uuid)
		if err != nil {
			respondWithError(w, http.StatusNotFound, "Chirp not found")
			return
		}

		chirp := Chirp{
			ID:        chirpDB.ID,
			CreatedAt: chirpDB.CreatedAt,
			UpdatedAt: chirpDB.UpdatedAt,
			Body:      chirpDB.Body,
			UserID:    chirpDB.UserID,
		}

		respondWithJSON(w, http.StatusOK, chirp)
	})

	s := &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	err = s.ListenAndServe()
	if err != nil {
		log.Fatal(err)
	}
}

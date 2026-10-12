// Package all bindet alle Treiber ein: cmd/monika importiert es, jeder Treiber registriert sich
// in seinem init() (sdk.Register). Ein neuer Treiber kommt hier als Blank-Import dazu.
package all

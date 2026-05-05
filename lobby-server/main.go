package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "open-match.dev/open-match/pkg/pb"
)

var frontendClient pb.FrontendServiceClient

func main() {
	conn, err := grpc.Dial("localhost:50504", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("OpenMatch 연결 실패: %v", err)
	}
	defer conn.Close()
	frontendClient = pb.NewFrontendServiceClient(conn)

	http.HandleFunc("/match", matchHandler)
	http.HandleFunc("/ticket/", ticketHandler)
	http.HandleFunc("/cancel/", cancelHandler)

	log.Println("로비 서버 시작 :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func matchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST만 허용", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 티켓 생성
	ticket, err := frontendClient.CreateTicket(ctx, &pb.CreateTicketRequest{
		Ticket: &pb.Ticket{
			SearchFields: &pb.SearchFields{
				Tags: []string{"mode:deathmatch"},
			},
		},
	})
	if err != nil {
		http.Error(w, "티켓 생성 실패: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("티켓 생성됨: %s", ticket.Id)

	// 30초 후 티켓 자동 만료
	go func() {
		time.Sleep(30 * time.Second)
		checkCtx, checkCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer checkCancel()

		t, err := frontendClient.GetTicket(checkCtx, &pb.GetTicketRequest{TicketId: ticket.Id})
		if err != nil {
			return
		}
		if t.Assignment == nil {
			deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer deleteCancel()
			frontendClient.DeleteTicket(deleteCtx, &pb.DeleteTicketRequest{TicketId: ticket.Id})
			log.Printf("티켓 만료 삭제: %s", ticket.Id)
		}
	}()

	// 매칭 완료될 때까지 폴링 (최대 30초)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(1 * time.Second)

		pollCtx, pollCancel := context.WithTimeout(context.Background(), 5*time.Second)
		t, err := frontendClient.GetTicket(pollCtx, &pb.GetTicketRequest{TicketId: ticket.Id})
		pollCancel()

		if err != nil {
			// 티켓이 삭제됨 (만료)
			http.Error(w, "매칭 시간 초과", http.StatusRequestTimeout)
			return
		}

		if t.Assignment != nil {
			// 매칭 완료!
			log.Printf("매칭 완료: %s → %s", ticket.Id, t.Assignment.Connection)
			json.NewEncoder(w).Encode(map[string]string{
				"ticket_id":  ticket.Id,
				"status":     "매칭완료",
				"connection": t.Assignment.Connection,
			})
			return
		}

		log.Printf("매칭 대기중: %s", ticket.Id)
	}

	// 30초 지나도 매칭 안 됨
	http.Error(w, "매칭 시간 초과", http.StatusRequestTimeout)
}

func ticketHandler(w http.ResponseWriter, r *http.Request) {
	ticketId := r.URL.Path[len("/ticket/"):]
	if ticketId == "" {
		http.Error(w, "ticket_id 필요", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ticket, err := frontendClient.GetTicket(ctx, &pb.GetTicketRequest{TicketId: ticketId})
	if err != nil {
		http.Error(w, "티켓 없음 (만료됐거나 존재하지 않음)", http.StatusNotFound)
		return
	}

	response := map[string]interface{}{
		"ticket_id":     ticket.Id,
		"search_fields": ticket.SearchFields,
	}

	if ticket.Assignment != nil {
		response["assignment"] = map[string]string{
			"connection": ticket.Assignment.Connection,
		}
		response["status"] = "매칭완료"
	} else {
		response["status"] = "대기중"
	}

	json.NewEncoder(w).Encode(response)
}

func cancelHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "DELETE만 허용", http.StatusMethodNotAllowed)
		return
	}

	ticketId := r.URL.Path[len("/cancel/"):]
	if ticketId == "" {
		http.Error(w, "ticket_id 필요", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := frontendClient.DeleteTicket(ctx, &pb.DeleteTicketRequest{TicketId: ticketId})
	if err != nil {
		http.Error(w, "취소 실패: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("티켓 취소됨: %s", ticketId)
	json.NewEncoder(w).Encode(map[string]string{
		"ticket_id": ticketId,
		"status":    "취소됨",
	})
}

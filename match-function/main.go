package main

import (
	"fmt"
	"log"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	mmf "open-match.dev/open-match/pkg/matchfunction"
	pb "open-match.dev/open-match/pkg/pb"
)

const (
	matchName        = "deathmatch-2players"
	playersPerMatch  = 2
	queryServiceAddr = "open-match-query.open-match.svc.cluster.local:50503"
)

type matchFunctionService struct {
	queryClient pb.QueryServiceClient
}

func (s *matchFunctionService) Run(req *pb.RunRequest, stream pb.MatchFunction_RunServer) error {
	tickets, err := mmf.QueryPool(stream.Context(), s.queryClient, req.GetProfile().Pools[0])
	if err != nil {
		return fmt.Errorf("티켓 조회 실패: %v", err)
	}

	log.Printf("대기 중인 티켓 수: %d", len(tickets))

	for i := 0; i+playersPerMatch <= len(tickets); i += playersPerMatch {
		match := &pb.Match{
			MatchId:       fmt.Sprintf("%s-%d", matchName, i),
			MatchProfile:  req.GetProfile().Name,
			MatchFunction: matchName,
			Tickets:       tickets[i : i+playersPerMatch],
		}

		log.Printf("매치 생성: %s (플레이어 %d명)", match.MatchId, len(match.Tickets))

		if err := stream.Send(&pb.RunResponse{Proposal: match}); err != nil {
			return fmt.Errorf("매치 전송 실패: %v", err)
		}
	}

	return nil
}

func main() {
	queryConn, err := grpc.Dial(queryServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Query 서비스 연결 실패: %v", err)
	}
	defer queryConn.Close()

	lis, err := net.Listen("tcp", ":50502")
	if err != nil {
		log.Fatalf("포트 열기 실패: %v", err)
	}

	server := grpc.NewServer()
	pb.RegisterMatchFunctionServer(server, &matchFunctionService{
		queryClient: pb.NewQueryServiceClient(queryConn),
	})

	log.Println("Match Function 시작 :50502")
	if err := server.Serve(lis); err != nil {
		log.Fatalf("서버 실행 실패: %v", err)
	}
}

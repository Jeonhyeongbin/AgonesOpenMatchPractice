package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "open-match.dev/open-match/pkg/pb"

	agonesv1 "agones.dev/agones/pkg/apis/agones/v1"
	allocationv1 "agones.dev/agones/pkg/apis/allocation/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"path/filepath"

	agones "agones.dev/agones/pkg/client/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	backendAddr    = "localhost:50505"
	mmfHost        = "match-function.open-match.svc.cluster.local"
	mmfPort        = 50502
	namespace      = "default"
)

func main() {
	// OpenMatch Backend 연결
	conn, err := grpc.Dial(backendAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Backend 연결 실패: %v", err)
	}
	defer conn.Close()
	backendClient := pb.NewBackendServiceClient(conn)

	// Agones 클라이언트 연결
	agonesClient, err := createAgonesClient()
	if err != nil {
		log.Fatalf("Agones 연결 실패: %v", err)
	}

	log.Println("Director 시작!")

	// 5초마다 매치 확인
	for {
		if err := fetchAndAllocate(backendClient, agonesClient); err != nil {
			log.Printf("오류: %v", err)
		}
		time.Sleep(5 * time.Second)
	}
}

func fetchAndAllocate(backendClient pb.BackendServiceClient, agonesClient *agones.Clientset) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// OpenMatch에 매치 요청
	stream, err := backendClient.FetchMatches(ctx, &pb.FetchMatchesRequest{
		Config: &pb.FunctionConfig{
			Host: mmfHost,
			Port: int32(mmfPort),
			Type: pb.FunctionConfig_GRPC,
		},
		Profile: &pb.MatchProfile{
			Name: "deathmatch-profile",
			Pools: []*pb.Pool{
				{
					Name: "deathmatch-pool",
					TagPresentFilters: []*pb.TagPresentFilter{
						{Tag: "mode:deathmatch"},
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("FetchMatches 실패: %v", err)
	}

	for {
		resp, err := stream.Recv()
		if err != nil {
			break
		}

		match := resp.Match
		log.Printf("매치 수신: %s (플레이어 %d명)", match.MatchId, len(match.Tickets))

		// Agones에 서버 배정 요청
		addr, port, err := allocateGameServer(agonesClient)
		if err != nil {
			log.Printf("서버 배정 실패: %v", err)
			continue
		}

		log.Printf("서버 배정 완료: %s:%d", addr, port)

		// 티켓에 서버 주소 배정
		assignments := make([]*pb.AssignmentGroup, 0)
		ticketIds := make([]string, 0)
		for _, ticket := range match.Tickets {
			ticketIds = append(ticketIds, ticket.Id)
		}
		assignments = append(assignments, &pb.AssignmentGroup{
			TicketIds: ticketIds,
			Assignment: &pb.Assignment{
				Connection: fmt.Sprintf("%s:%d", addr, port),
			},
		})

		_, err = backendClient.AssignTickets(context.Background(), &pb.AssignTicketsRequest{
			Assignments: assignments,
		})
		if err != nil {
			log.Printf("티켓 배정 실패: %v", err)
		} else {
			log.Printf("플레이어들에게 서버 주소 배정 완료: %s:%d", addr, port)
		}
	}

	return nil
}

func allocateGameServer(agonesClient *agones.Clientset) (string, int32, error) {
	allocation := &allocationv1.GameServerAllocation{
		Spec: allocationv1.GameServerAllocationSpec{
			Selectors: []allocationv1.GameServerSelector{
				{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							agonesv1.FleetNameLabel: "xonotic",
						},
					},
				},
			},
		},
	}

	result, err := agonesClient.AllocationV1().GameServerAllocations(namespace).Create(
		context.Background(), allocation, metav1.CreateOptions{},
	)
	if err != nil {
		return "", 0, fmt.Errorf("Allocation 실패: %v", err)
	}

	if result.Status.State != allocationv1.GameServerAllocationAllocated {
		return "", 0, fmt.Errorf("서버 없음")
	}

	return result.Status.Address, result.Status.Ports[0].Port, nil
}

func createAgonesClient() (*agones.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		// 로컬 환경에서는 kubeconfig 사용
		home := homedir.HomeDir()
		kubeconfig := filepath.Join(home, ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, err
		}
	}
	return agones.NewForConfig(config)
}

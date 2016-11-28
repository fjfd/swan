package manager

import (
	"fmt"
	"strings"
	"sync"

	"github.com/Dataman-Cloud/swan/src/manager/apiserver"
	"github.com/Dataman-Cloud/swan/src/manager/ipam"
	"github.com/Dataman-Cloud/swan/src/manager/ns"
	"github.com/Dataman-Cloud/swan/src/manager/raft"
	"github.com/Dataman-Cloud/swan/src/manager/sched"
	"github.com/Dataman-Cloud/swan/src/manager/swancontext"
	. "github.com/Dataman-Cloud/swan/src/store/local"
	"github.com/Dataman-Cloud/swan/src/util"
	"github.com/boltdb/bolt"
	events "github.com/docker/go-events"

	"github.com/Sirupsen/logrus"
	"golang.org/x/net/context"
)

type Manager struct {
	store     *BoltStore
	apiserver *apiserver.ApiServer
	//proxyserver

	ipamAdapter *ipam.IpamAdapter
	resolver    *ns.Resolver
	sched       *sched.Sched
	raftNode    *raft.Node

	swanContext *swancontext.SwanContext
	config      util.SwanConfig
}

func New(config util.SwanConfig, db *bolt.DB) (*Manager, error) {
	manager := &Manager{
		config: config,
	}

	store, err := NewBoltStore(db)
	if err != nil {
		logrus.Errorf("Init store engine failed:%s", err)
		return nil, err
	}

	manager.swanContext = &swancontext.SwanContext{
		Config: config,
		Store:  store,
		ApiServer: apiserver.NewApiServer(manager.config.HttpListener.TCPAddr,
			manager.config.HttpListener.UnixAddr),
	}

	manager.ipamAdapter, err = ipam.New(manager.swanContext)
	if err != nil {
		logrus.Errorf("init ipam adapter failed. Error: ", err.Error())
		return nil, err
	}

	manager.resolver = ns.New(manager.config.DNS)
	manager.sched = sched.New(manager.config.Scheduler, manager.swanContext)

	raftNode, err := raft.NewNode(config.Raft.RaftId, strings.Split(config.Raft.Cluster, ","), db)
	if err != nil {
		logrus.Errorf("inti raft node failed. Error: %s", err.Error())
		return nil, err
	}
	manager.raftNode = raftNode

	return manager, nil
}

func (manager *Manager) Stop() error {
	return nil
}

func (manager *Manager) Start() error {
	var wg sync.WaitGroup
	var err error
	wg.Add(4)

	go func() {
		err = manager.resolver.Start()
		wg.Done()
	}()

	go func() {
		err = manager.sched.Start()
		wg.Done()
	}()

	go func() {
		err = manager.swanContext.ApiServer.ListenAndServe()
		wg.Done()
	}()

	go func() {
		err = manager.ipamAdapter.Start()
		wg.Done()
	}()

	wg.Wait()

	leadershipCh, cancel := manager.raftNode.SubscribeLeadership()
	defer cancel()

	go handleLeadershipEvents(context.TODO(), leadershipCh)

	ctx := context.Background()
	go func() {
		err := manager.raftNode.StartRaft(ctx)
		if err != nil {
			logrus.Fatal(err)
		}
	}()

	if err := manager.raftNode.WaitForLeader(ctx); err != nil {
		return err
	}

	return err
}

func handleLeadershipEvents(ctx context.Context, leadershipCh chan events.Event) {
	for {
		select {
		case leadershipEvent := <-leadershipCh:
			// TODO lock it and if manager stop return
			newState := leadershipEvent.(raft.LeadershipState)

			if newState == raft.IsLeader {
				fmt.Println("Now i am a leader !!!!!")
			} else if newState == raft.IsFollower {
				fmt.Println("Now i am a follower !!!!!")
			}
		case <-ctx.Done():
			return
		}
	}
}
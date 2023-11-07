package arangodb

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	driver "github.com/arangodb/go-driver"
	"github.com/golang/glog"
	"github.com/jalapeno/linkstate-edge-v6/pkg/kafkanotifier"
	"github.com/sbezverk/gobmp/pkg/base"
	"github.com/sbezverk/gobmp/pkg/message"
)

func (a *arangoDB) lsLinkHandler(obj *kafkanotifier.EventMessage) error {
	ctx := context.TODO()
	if obj == nil {
		return fmt.Errorf("event message is nil")
	}
	// Check if Collection encoded in ID exists
	c := strings.Split(obj.ID, "/")[0]
	if strings.Compare(c, a.lslink.Name()) != 0 {
		return fmt.Errorf("configured collection name %s and received in event collection name %s do not match", a.lslink.Name(), c)
	}
	//glog.V(5).Infof("Processing action: %s for key: %s ID: %s", obj.Action, obj.Key, obj.ID)
	var o message.LSLink
	_, err := a.lslink.ReadDocument(ctx, obj.Key, &o)
	if err != nil {
		// In case of a ls_link removal notification, reading it will return Not Found error
		if !driver.IsNotFound(err) {
			return fmt.Errorf("failed to read existing document %s with error: %+v", obj.Key, err)
		}
		// If operation matches to "del" then it is confirmed delete operation, otherwise return error
		if obj.Action != "del" {
			return fmt.Errorf("document %s not found but Action is not \"del\", possible stale event", obj.Key)
		}
		err := a.processLinkRemoval(ctx, obj.Key, obj.Action)
		if err != nil {
			return err
		}
		// write event into ls_node_edge topic
		a.notifier.EventNotification(obj)
		return nil
	}
	switch obj.Action {
	case "add":
		fallthrough
	case "update":
		if err := a.processLSLinkEdge(ctx, obj.Key, &o); err != nil {
			return fmt.Errorf("failed to process action %s for edge %s with error: %+v", obj.Action, obj.Key, err)
		}
	}
	//glog.V(5).Infof("Complete processing action: %s for key: %s ID: %s", obj.Action, obj.Key, obj.ID)

	// write event into ls_topoogy_v4 topic
	a.notifier.EventNotification(obj)

	return nil
}

// processEdge processes a single ls_link connection which is a unidirectional edge between two nodes (vertices).
func (a *arangoDB) processLSLinkEdge(ctx context.Context, key string, l *message.LSLink) error {
	if l.ProtocolID == base.BGP {
		return nil
	}
	if l.MTID == nil {
		return nil
	}
	glog.Infof("processEdge processing lslink: %s", l.ID)
	// get local node from ls_link entry
	ln, err := a.getNode(ctx, l, true)
	if err != nil {
		glog.Errorf("processEdge failed to get local lsnode %s for link: %s with error: %+v", l.IGPRouterID, l.ID, err)
		return err
	}

	// get remote node from ls_link entry
	rn, err := a.getNode(ctx, l, false)
	if err != nil {
		glog.Errorf("processEdge failed to get remote lsnode %s for link: %s with error: %+v", l.RemoteIGPRouterID, l.ID, err)
		return err
	}
	glog.V(6).Infof("Local node -> Protocol: %+v Domain ID: %+v IGP Router ID: %+v", ln.ProtocolID, ln.DomainID, ln.IGPRouterID)
	glog.V(6).Infof("Remote node -> Protocol: %+v Domain ID: %+v IGP Router ID: %+v", rn.ProtocolID, rn.DomainID, rn.IGPRouterID)
	if err := a.createEdgeObject(ctx, l, ln, rn); err != nil {
		glog.Errorf("processEdge failed to create Edge object with error: %+v", err)
		glog.Errorf("Local node -> Protocol: %+v Domain ID: %+v IGP Router ID: %+v", ln.ProtocolID, ln.DomainID, ln.IGPRouterID)
		glog.Errorf("Remote node -> Protocol: %+v Domain ID: %+v IGP Router ID: %+v", rn.ProtocolID, rn.DomainID, rn.IGPRouterID)
		return err
	}
	//glog.V(9).Infof("processEdge completed processing lslink: %s for ls nodes: %s - %s", l.ID, ln.ID, rn.ID)

	return nil
}

// processEdgeRemoval removes a record from Node's graph collection
// since the key matches in both collections (LS Links and Nodes' Graph) deleting the record directly.
func (a *arangoDB) processLinkRemoval(ctx context.Context, key string, action string) error {
	if _, err := a.graph.RemoveDocument(ctx, key); err != nil {
		glog.Infof("removing edge %s", key)
		if !driver.IsNotFound(err) {
			return err
		}
		return nil
	}

	return nil
}

func (a *arangoDB) getNode(ctx context.Context, e *message.LSLink, local bool) (*message.LSNode, error) {
	// Need to find ls_node object matching ls_link's IGP Router ID
	query := "FOR d IN ls_node_extended " //+ a.lsnodeExt.Name()
	if local {
		//glog.Infof("getNode local node per link: %s, %s, %v", e.IGPRouterID, e.ID, e.ProtocolID)
		query += " filter d.igp_router_id == " + "\"" + e.IGPRouterID + "\""
	} else {
		//glog.Infof("getNode remote node per link: %s, %s, %v", e.RemoteIGPRouterID, e.ID, e.ProtocolID)
		query += " filter d.igp_router_id == " + "\"" + e.RemoteIGPRouterID + "\""
	}
	query += " filter d.domain_id == " + strconv.Itoa(int(e.DomainID))

	// If OSPFv2 or OSPFv3, then query must include AreaID
	if e.ProtocolID == base.OSPFv2 || e.ProtocolID == base.OSPFv3 {
		query += " filter d.area_id == " + "\"" + e.AreaID + "\""
	}
	query += " return d"
	//glog.Infof("query: %s", query)
	lcursor, err := a.db.Query(ctx, query, nil)
	if err != nil {
		return nil, err
	}
	defer lcursor.Close()
	var ln message.LSNode
	i := 0
	for ; ; i++ {
		_, err := lcursor.ReadDocument(ctx, &ln)
		if err != nil {
			if !driver.IsNoMoreDocuments(err) {
				return nil, err
			}
			break
		}
	}
	if i == 0 {
		return nil, fmt.Errorf("query %s returned 0 results", query)
	}
	if i > 1 {
		return nil, fmt.Errorf("query %s returned more than 1 result", query)
	}

	return &ln, nil
}

func (a *arangoDB) createEdgeObject(ctx context.Context, l *message.LSLink, ln, rn *message.LSNode) error {
	mtid := 0
	if l.MTID != nil {
		mtid = int(l.MTID.MTID)
	}
	ne := lsTopologyObject{
		Key:                   l.Key,
		From:                  ln.ID,
		To:                    rn.ID,
		Link:                  l.Key,
		ProtocolID:            l.ProtocolID,
		DomainID:              l.DomainID,
		MTID:                  uint16(mtid),
		AreaID:                l.AreaID,
		Protocol:              rn.Protocol,
		LocalLinkID:           l.LocalLinkID,
		RemoteLinkID:          l.RemoteLinkID,
		LocalLinkIP:           l.LocalLinkIP,
		RemoteLinkIP:          l.RemoteLinkIP,
		LocalNodeASN:          l.LocalNodeASN,
		RemoteNodeASN:         l.RemoteNodeASN,
		PeerNodeSID:           l.PeerNodeSID,
		SRv6BGPPeerNodeSID:    l.SRv6BGPPeerNodeSID,
		SRv6ENDXSID:           l.SRv6ENDXSID,
		LSAdjacencySID:        l.LSAdjacencySID,
		UnidirLinkDelay:       l.UnidirLinkDelay,
		UnidirLinkDelayMinMax: l.UnidirLinkDelayMinMax,
		UnidirDelayVariation:  l.UnidirDelayVariation,
		UnidirPacketLoss:      l.UnidirPacketLoss,
		UnidirResidualBW:      l.UnidirResidualBW,
		UnidirAvailableBW:     l.UnidirAvailableBW,
		UnidirBWUtilization:   l.UnidirBWUtilization,
	}
	if _, err := a.graph.CreateDocument(ctx, &ne); err != nil {
		if !driver.IsConflict(err) {
			return err
		}
		// The document already exists, updating it with the latest info
		if _, err := a.graph.UpdateDocument(ctx, ne.Key, &ne); err != nil {
			return err
		}
	}

	return nil
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	retrygo "github.com/avast/retry-go/v4"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/redis/go-redis/v9"
	amqp "github.com/rabbitmq/amqp091-go"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	k8sretry "k8s.io/client-go/util/retry"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type FaultType string

const (
	FaultPodCrash     FaultType = "PodCrashLooping"
	FaultOOMKilled    FaultType = "OOMKilled"
	FaultNodeNotReady FaultType = "NodeNotReady"
	FaultImagePull    FaultType = "ImagePullBackOff"
	FaultConfigError  FaultType = "ConfigMapError"
	FaultServicePort  FaultType = "ServicePortError"
	FaultUnknown      FaultType = "Unknown"
)

type AgentConfig struct {
	ListenAddr       string
	RabbitMQURL      string
	RedisAddr        string
	RedisPassword    string
	OllamaURL        string
	OllamaModel      string
	RAGRetrieveURL   string
	DingTalkWebhook  string
	ElasticsearchURL string
	MySQLDSN         string
	PrometheusURL    string
	EnablePolling    bool
	PollInterval     time.Duration
	DryRun           bool
}

type AlertmanagerWebhook struct {
	Version  string    `json:"version"`
	GroupKey string    `json:"groupKey"`
	Status   string    `json:"status"`
	Alerts   []AMAlert `json:"alerts"`
}

type AMAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
	EndsAt      time.Time         `json:"endsAt"`
}

type PrometheusAlertsResponse struct {
	Status string `json:"status"`
	Data   struct {
		Alerts []struct {
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
			State       string            `json:"state"`
			ActiveAt    time.Time         `json:"activeAt"`
		} `json:"alerts"`
	} `json:"data"`
}

type RAGRetrieveResponse struct {
	Query   string   `json:"query"`
	Context string   `json:"context"`
	Sources []string `json:"sources"`
}

type OllamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type OllamaResponse struct {
	Response string `json:"response"`
}

type LLMDecision struct {
	Action     string `json:"action"`
	TargetKind string `json:"target_kind"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	Reason     string `json:"reason"`
	RiskLevel  string `json:"risk_level"`
	Message    string `json:"message"`
}

type FaultRecord struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	FaultID     string    `gorm:"index;size:255" json:"fault_id"`
	AlertName   string    `gorm:"size:128" json:"alert_name"`
	Namespace   string    `gorm:"size:128" json:"namespace"`
	Name        string    `gorm:"size:255" json:"name"`
	Action      string    `gorm:"size:128" json:"action"`
	Status      string    `gorm:"size:64" json:"status"`
	Message     string    `gorm:"type:text" json:"message"`
	RawAlert    string    `gorm:"type:json" json:"raw_alert"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
}

var (
	cfg         AgentConfig
	kubeClient *kubernetes.Clientset
	redisClient *redis.Client
	esClient    *elasticsearch.Client
	mqConn      *amqp.Connection
	mqChan      *amqp.Channel
	db          *gorm.DB
	httpClient  = &http.Client{Timeout: 20 * time.Second}
)

func getenv(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" { return fallback }
	return v
}

func loadConfig() AgentConfig {
	polling := strings.EqualFold(getenv("ENABLE_PROM_POLLING", "false"), "true")
	dryRun := strings.EqualFold(getenv("DRY_RUN", "false"), "true")
	return AgentConfig{
		ListenAddr:       getenv("LISTEN_ADDR", ":8080"),
		RabbitMQURL:      getenv("RABBITMQ_URL", "amqp://user:password@rabbitmq.middleware.svc.cluster.local:5672/"),
		RedisAddr:        getenv("REDIS_ADDR", "redis-master.middleware.svc.cluster.local:6379"),
		RedisPassword:    getenv("REDIS_PASSWORD", ""),
		OllamaURL:        getenv("OLLAMA_URL", "http://ollama-service.ai-services.svc.cluster.local:11434/api/generate"),
		OllamaModel:      getenv("OLLAMA_MODEL", "qwen2:7b-instruct"),
		RAGRetrieveURL:   getenv("RAG_RETRIEVE_URL", "http://sre-rag-service.sre-assistant.svc.cluster.local:8000/retrieve"),
		DingTalkWebhook:  getenv("DINGTALK_WEBHOOK", ""),
		ElasticsearchURL: getenv("ELASTICSEARCH_URL", "http://elasticsearch.middleware.svc.cluster.local:9200"),
		MySQLDSN:         getenv("MYSQL_DSN", "sre:srepass@tcp(mysql.middleware.svc.cluster.local:3306)/sre_assistant?charset=utf8mb4&parseTime=True&loc=Local"),
		PrometheusURL:    getenv("PROMETHEUS_URL", "http://prometheus-stack-kube-prom-prometheus.monitoring.svc.cluster.local:9090"),
		EnablePolling:    polling,
		PollInterval:     30 * time.Second,
		DryRun:           dryRun,
	}
}

func initKubeClient() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil { return nil, err }
	}
	return kubernetes.NewForConfig(config)
}

func initClients() error {
	var err error
	cfg = loadConfig()
	kubeClient, err = initKubeClient()
	if err != nil { return fmt.Errorf("init kube client: %w", err) }

	redisClient = redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: 0})
	if err := redisClient.Ping(context.Background()).Err(); err != nil { return fmt.Errorf("init redis: %w", err) }

	mqConn, err = amqp.Dial(cfg.RabbitMQURL)
	if err != nil { return fmt.Errorf("init rabbitmq: %w", err) }
	mqChan, err = mqConn.Channel()
	if err != nil { return err }
	_, err = mqChan.QueueDeclare("sre-alerts", true, false, false, false, nil)
	if err != nil { return err }
	if err := mqChan.Qos(1, 0, false); err != nil { return err }

	esClient, err = elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{cfg.ElasticsearchURL}})
	if err != nil { return fmt.Errorf("init elasticsearch: %w", err) }

	db, err = gorm.Open(mysql.Open(cfg.MySQLDSN), &gorm.Config{})
	if err != nil { return fmt.Errorf("init mysql: %w", err) }
	if err := db.AutoMigrate(&FaultRecord{}); err != nil { return err }
	return nil
}

func main() {
	log.Println("SRE Agent starting...")
	if err := initClients(); err != nil { log.Fatalf("init failed: %v", err) }
	defer mqConn.Close()
	defer mqChan.Close()

	go consumeAlerts()
	if cfg.EnablePolling { go pollPrometheusLoop() }

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	http.HandleFunc("/alertmanager/webhook", alertmanagerWebhookHandler)
	log.Printf("HTTP server listening on %s", cfg.ListenAddr)
	log.Fatal(http.ListenAndServe(cfg.ListenAddr, nil))
}

func alertmanagerWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Error(w, "method not allowed", http.StatusMethodNotAllowed); return }
	body, err := io.ReadAll(r.Body)
	if err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }
	var payload AlertmanagerWebhook
	if err := json.Unmarshal(body, &payload); err != nil { http.Error(w, err.Error(), http.StatusBadRequest); return }
	count := 0
	for _, alert := range payload.Alerts {
		if alert.Status != "firing" { continue }
		if err := publishAlert(alert); err != nil { log.Printf("publish alert failed: %v", err); continue }
		count++
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status":"ok", "published":count})
}

func publishAlert(alert AMAlert) error {
	body, _ := json.Marshal(alert)
	return mqChan.PublishWithContext(context.Background(), "", "sre-alerts", false, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent,
		ContentType: "application/json",
		Body: body,
		Timestamp: time.Now(),
	})
}

func consumeAlerts() {
	msgs, err := mqChan.Consume("sre-alerts", "sre-agent-worker", false, false, false, false, nil)
	if err != nil { log.Fatalf("consume rabbitmq failed: %v", err) }
	for msg := range msgs {
		var alert AMAlert
		if err := json.Unmarshal(msg.Body, &alert); err != nil { log.Printf("bad message: %v", err); _ = msg.Nack(false, false); continue }
		if err := processAlert(alert); err != nil {
			log.Printf("process alert failed: %v", err)
			_ = msg.Nack(false, true)
			continue
		}
		_ = msg.Ack(false)
	}
}

func pollPrometheusLoop() {
	for {
		alerts, err := queryPrometheusAlerts()
		if err != nil { log.Printf("query prometheus failed: %v", err); time.Sleep(cfg.PollInterval); continue }
		for _, a := range alerts { _ = publishAlert(a) }
		time.Sleep(cfg.PollInterval)
	}
}

func queryPrometheusAlerts() ([]AMAlert, error) {
	resp, err := httpClient.Get(strings.TrimRight(cfg.PrometheusURL, "/") + "/api/v1/alerts")
	if err != nil { return nil, err }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var parsed PrometheusAlertsResponse
	if err := json.Unmarshal(body, &parsed); err != nil { return nil, err }
	var result []AMAlert
	for _, a := range parsed.Data.Alerts {
		if a.State == "firing" {
			result = append(result, AMAlert{Status:"firing", Labels:a.Labels, Annotations:a.Annotations, StartsAt:a.ActiveAt})
		}
	}
	return result, nil
}

func processAlert(alert AMAlert) error {
	fault := parseFaultType(alert)
	ns, name := parseTarget(alert, fault)
	faultID := fmt.Sprintf("%s:%s:%s", fault, ns, name)
	if ok, err := lockFault(faultID); err != nil || !ok {
		log.Printf("skip duplicated fault %s", faultID)
		return nil
	}

	raw, _ := json.Marshal(alert)
	rec := FaultRecord{FaultID:faultID, AlertName:string(fault), Namespace:ns, Name:name, Status:"running", RawAlert:string(raw), StartedAt:time.Now()}
	_ = db.Create(&rec).Error
	logToES("info", "start processing fault", map[string]any{"fault_id":faultID, "namespace":ns, "name":name, "fault":fault})

	decision, err := decideWithRAGAndLLM(fault, alert, ns, name)
	if err != nil {
		finishRecord(&rec, "failed", "DECIDE_FAILED", err.Error())
		return err
	}
	if decision.Namespace == "" { decision.Namespace = ns }
	if decision.Name == "" { decision.Name = name }

	err = executeDecision(*decision)
	if err != nil {
		finishRecord(&rec, "failed", decision.Action, err.Error())
		logToES("error", "execute decision failed", map[string]any{"fault_id":faultID, "action":decision.Action, "error":err.Error()})
		return err
	}
	msg := fmt.Sprintf("故障处理完成：fault=%s target=%s/%s action=%s reason=%s", fault, ns, name, decision.Action, decision.Reason)
	finishRecord(&rec, "success", decision.Action, msg)
	logToES("info", msg, map[string]any{"fault_id":faultID, "action":decision.Action})
	_ = sendDingTalk(msg)
	return nil
}

func finishRecord(rec *FaultRecord, status, action, message string) {
	rec.Status = status
	rec.Action = action
	rec.Message = message
	rec.FinishedAt = time.Now()
	_ = db.Save(rec).Error
}

func parseFaultType(alert AMAlert) FaultType {
	name := alert.Labels["alertname"]
	switch name {
	case string(FaultPodCrash): return FaultPodCrash
	case string(FaultOOMKilled): return FaultOOMKilled
	case string(FaultNodeNotReady): return FaultNodeNotReady
	case string(FaultImagePull): return FaultImagePull
	case string(FaultConfigError): return FaultConfigError
	case string(FaultServicePort): return FaultServicePort
	default:
		if strings.Contains(strings.ToLower(name), "crash") { return FaultPodCrash }
		if strings.Contains(strings.ToLower(name), "oom") { return FaultOOMKilled }
		if strings.Contains(strings.ToLower(name), "imagepull") { return FaultImagePull }
		return FaultUnknown
	}
}

func parseTarget(alert AMAlert, fault FaultType) (namespace, name string) {
	namespace = firstNonEmpty(alert.Labels["namespace"], "default")
	switch fault {
	case FaultNodeNotReady:
		name = firstNonEmpty(alert.Labels["node"], alert.Labels["instance"])
	case FaultImagePull, FaultPodCrash, FaultOOMKilled, FaultConfigError:
		name = firstNonEmpty(alert.Labels["pod"], alert.Labels["pod_name"], alert.Labels["name"])
	case FaultServicePort:
		name = firstNonEmpty(alert.Labels["service"], alert.Labels["svc"], alert.Labels["name"])
	default:
		name = firstNonEmpty(alert.Labels["pod"], alert.Labels["node"], alert.Labels["name"])
	}
	return namespace, name
}

func firstNonEmpty(values ...string) string {
	for _, v := range values { if strings.TrimSpace(v) != "" { return strings.TrimSpace(v) } }
	return ""
}

func lockFault(faultID string) (bool, error) {
	return redisClient.SetNX(context.Background(), "lock:"+faultID, "1", 5*time.Minute).Result()
}

func decideWithRAGAndLLM(fault FaultType, alert AMAlert, namespace, name string) (*LLMDecision, error) {
	cacheKey := fmt.Sprintf("llm_decision:%s:%s:%s", fault, namespace, name)
	if cached, err := redisClient.Get(context.Background(), cacheKey).Result(); err == nil {
		var d LLMDecision
		if json.Unmarshal([]byte(cached), &d) == nil { return &d, nil }
	}

	_ = rateLimitLLM()
	contextText := buildFaultContext(alert, namespace, name)
	ragContext := retrieveRAG(fmt.Sprintf("%s %s", fault, contextText))
	prompt := buildPrompt(fault, contextText, ragContext)
	decision, err := callOllama(prompt)
	if err != nil { return fallbackDecision(fault, namespace, name, err.Error()), nil }
	if decision.Action == "" { decision = fallbackDecision(fault, namespace, name, "empty llm action") }
	b, _ := json.Marshal(decision)
	_ = redisClient.Set(context.Background(), cacheKey, string(b), 10*time.Minute).Err()
	return decision, nil
}

func rateLimitLLM() error {
	key := "llm_limiter:" + time.Now().Format("20060102150405")
	count, err := redisClient.Incr(context.Background(), key).Result()
	if err == nil && count == 1 { _ = redisClient.Expire(context.Background(), key, 2*time.Second).Err() }
	if count > 1 { time.Sleep(time.Second) }
	return err
}

func buildFaultContext(alert AMAlert, namespace, name string) string {
	b, _ := json.MarshalIndent(map[string]any{"namespace":namespace,"name":name,"labels":alert.Labels,"annotations":alert.Annotations,"startsAt":alert.StartsAt}, "", "  ")
	return string(b)
}

func retrieveRAG(query string) string {
	payload, _ := json.Marshal(map[string]string{"query": query})
	resp, err := httpClient.Post(cfg.RAGRetrieveURL, "application/json", bytes.NewReader(payload))
	if err != nil { return "" }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var rr RAGRetrieveResponse
	if json.Unmarshal(body, &rr) != nil { return "" }
	return rr.Context
}

func buildPrompt(fault FaultType, faultContext, ragContext string) string {
	return fmt.Sprintf(`你是一个资深 Kubernetes SRE。请基于故障上下文和知识库，输出严格 JSON，不要 Markdown，不要解释。
允许的 action 只能是：RESTART_POD, EVICT_NODE, ROLLBACK_DEPLOYMENT, ROLLBACK_CONFIGMAP, DIAGNOSE_ONLY。
规则：
1. PodCrashLooping/OOMKilled 优先 RESTART_POD。
2. NodeNotReady 可 EVICT_NODE，但只对普通业务 Pod 驱逐，DaemonSet/static pod 不动。
3. ImagePullBackOff 优先 ROLLBACK_DEPLOYMENT，如果无法确定 deployment 则 DIAGNOSE_ONLY。
4. ConfigMapError 优先 ROLLBACK_CONFIGMAP。
5. 无把握时用 DIAGNOSE_ONLY。

返回格式：
{"action":"RESTART_POD","target_kind":"Pod","namespace":"default","name":"pod-name","risk_level":"low","reason":"原因","message":"给人的说明"}

故障类型：%s
故障上下文：%s
知识库参考：%s
`, fault, faultContext, ragContext)
}

func callOllama(prompt string) (*LLMDecision, error) {
	reqBody, _ := json.Marshal(OllamaRequest{Model:cfg.OllamaModel, Prompt:prompt, Stream:false})
	resp, err := httpClient.Post(cfg.OllamaURL, "application/json", bytes.NewReader(reqBody))
	if err != nil { return nil, err }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var or OllamaResponse
	if err := json.Unmarshal(body, &or); err != nil { return nil, err }
	jsonText := extractJSONObject(or.Response)
	var d LLMDecision
	if err := json.Unmarshal([]byte(jsonText), &d); err != nil { return nil, fmt.Errorf("parse llm json failed: %w; raw=%s", err, or.Response) }
	return &d, nil
}

func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start { return s[start:end+1] }
	return s
}

func fallbackDecision(fault FaultType, namespace, name, why string) *LLMDecision {
	d := &LLMDecision{Namespace:namespace, Name:name, RiskLevel:"low", Reason:"fallback: "+why}
	switch fault {
	case FaultPodCrash, FaultOOMKilled:
		d.Action = "RESTART_POD"; d.TargetKind = "Pod"; d.Message = "使用兜底策略重启 Pod"
	case FaultNodeNotReady:
		d.Action = "EVICT_NODE"; d.TargetKind = "Node"; d.Message = "使用兜底策略隔离并驱逐节点普通 Pod"
	case FaultConfigError:
		d.Action = "ROLLBACK_CONFIGMAP"; d.TargetKind = "ConfigMap"; d.Message = "使用兜底策略回滚 ConfigMap"
	default:
		d.Action = "DIAGNOSE_ONLY"; d.Message = "无法安全自动修复，只记录诊断"
	}
	return d
}

func executeDecision(d LLMDecision) error {
	if cfg.DryRun { log.Printf("DRY_RUN decision=%+v", d); return nil }
	return retrygo.Do(func() error {
		switch d.Action {
		case "RESTART_POD": return restartPod(d.Namespace, d.Name)
		case "EVICT_NODE": return evictNode(d.Name)
		case "ROLLBACK_DEPLOYMENT": return rollbackDeployment(d.Namespace, d.Name)
		case "ROLLBACK_CONFIGMAP": return rollbackConfigMap(d.Namespace, d.Name)
		case "DIAGNOSE_ONLY", "": return nil
		default: return fmt.Errorf("action not allowed: %s", d.Action)
		}
	}, retrygo.Attempts(3), retrygo.Delay(2*time.Second))
}

func restartPod(namespace, podName string) error {
	if namespace == "" || podName == "" { return errors.New("restart pod requires namespace and pod name") }
	return kubeClient.CoreV1().Pods(namespace).Delete(context.Background(), podName, metav1.DeleteOptions{})
}

func evictNode(nodeName string) error {
	if nodeName == "" { return errors.New("evict node requires node name") }
	ctx := context.Background()
	if err := k8sretry.RetryOnConflict(k8sretry.DefaultRetry, func() error {
		node, err := kubeClient.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if err != nil { return err }
		node.Spec.Unschedulable = true
		_, err = kubeClient.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
		return err
	}); err != nil { return err }

	pods, err := kubeClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + nodeName})
	if err != nil { return err }
	grace := int64(30)
	for _, p := range pods.Items {
		if shouldSkipEviction(p) { continue }
		eviction := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name:p.Name, Namespace:p.Namespace}, DeleteOptions:&metav1.DeleteOptions{GracePeriodSeconds:&grace}}
		if err := kubeClient.CoreV1().Pods(p.Namespace).EvictV1(ctx, eviction); err != nil {
			if apierrors.IsNotFound(err) { continue }
			log.Printf("evict pod %s/%s failed: %v", p.Namespace, p.Name, err)
		}
	}
	return nil
}

func shouldSkipEviction(p corev1.Pod) bool {
	if p.Namespace == "kube-system" { return true }
	if p.ObjectMeta.DeletionTimestamp != nil { return true }
	for _, owner := range p.OwnerReferences {
		if owner.Kind == "DaemonSet" { return true }
	}
	for _, owner := range p.OwnerReferences {
		if owner.Kind == "Node" { return true }
	}
	return false
}

func rollbackDeployment(namespace, deploymentName string) error {
	if namespace == "" || deploymentName == "" { return errors.New("rollback deployment requires namespace and deployment name") }
	ctx := context.Background()
	deploy, err := kubeClient.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil { return err }
	selector := labels.Set(deploy.Spec.Selector.MatchLabels).String()
	rsList, err := kubeClient.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{LabelSelector:selector})
	if err != nil { return err }
	var owned []appsv1.ReplicaSet
	for _, rs := range rsList.Items {
		for _, ref := range rs.OwnerReferences {
			if ref.Kind == "Deployment" && ref.Name == deploymentName { owned = append(owned, rs); break }
		}
	}
	if len(owned) < 2 { return fmt.Errorf("no previous replicaset found for deployment %s/%s", namespace, deploymentName) }
	sort.Slice(owned, func(i, j int) bool { return revisionOfRS(owned[i]) > revisionOfRS(owned[j]) })
	previous := owned[1]
	return k8sretry.RetryOnConflict(k8sretry.DefaultRetry, func() error {
		d, err := kubeClient.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil { return err }
		d.Spec.Template = previous.Spec.Template
		if d.Spec.Template.Annotations == nil { d.Spec.Template.Annotations = map[string]string{} }
		d.Spec.Template.Annotations["sre.assistant/rollback-at"] = time.Now().Format(time.RFC3339)
		_, err = kubeClient.AppsV1().Deployments(namespace).Update(ctx, d, metav1.UpdateOptions{})
		return err
	})
}

func revisionOfRS(rs appsv1.ReplicaSet) int64 {
	var rev int64
	_, _ = fmt.Sscanf(rs.Annotations["deployment.kubernetes.io/revision"], "%d", &rev)
	return rev
}

func rollbackConfigMap(namespace, cmName string) error {
	if namespace == "" || cmName == "" { return errors.New("rollback configmap requires namespace and configmap name") }
	ctx := context.Background()
	prev, err := kubeClient.CoreV1().ConfigMaps(namespace).Get(ctx, cmName+"-previous", metav1.GetOptions{})
	if err != nil { return fmt.Errorf("previous configmap %s-previous not found: %w", cmName, err) }
	return k8sretry.RetryOnConflict(k8sretry.DefaultRetry, func() error {
		cur, err := kubeClient.CoreV1().ConfigMaps(namespace).Get(ctx, cmName, metav1.GetOptions{})
		if err != nil { return err }
		cur.Data = prev.Data
		cur.BinaryData = prev.BinaryData
		if cur.Annotations == nil { cur.Annotations = map[string]string{} }
		cur.Annotations["sre.assistant/rollback-at"] = time.Now().Format(time.RFC3339)
		_, err = kubeClient.CoreV1().ConfigMaps(namespace).Update(ctx, cur, metav1.UpdateOptions{})
		return err
	})
}

func logToES(level, message string, fields map[string]any) {
	if fields == nil { fields = map[string]any{} }
	fields["@timestamp"] = time.Now().Format(time.RFC3339)
	fields["level"] = level
	fields["message"] = message
	body, _ := json.Marshal(fields)
	res, err := esClient.Index("sre-agent-logs", bytes.NewReader(body))
	if err != nil { log.Printf("write es failed: %v", err); return }
	_ = res.Body.Close()
}

func sendDingTalk(msg string) error {
	if cfg.DingTalkWebhook == "" { return nil }
	payload, _ := json.Marshal(map[string]any{"msgtype":"text", "text":map[string]string{"content":"【SRE智能运维助手】"+msg}})
	resp, err := httpClient.Post(cfg.DingTalkWebhook, "application/json", bytes.NewReader(payload))
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode >= 300 { b, _ := io.ReadAll(resp.Body); return fmt.Errorf("dingtalk status=%d body=%s", resp.StatusCode, string(b)) }
	return nil
}

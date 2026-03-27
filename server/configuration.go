package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	defaultRetentionDays       = 90
	defaultHotTopicLimit       = 10
	defaultReportKeywordLimit  = 20
	defaultAlertWindowHours    = 24
	defaultAlertSpikeThreshold = 5
	defaultReindexBatchSize    = 200
)

type configuration struct {
	Config string `json:"Config"`
}

type AnalysisScope struct {
	IncludedTeamIDs      []string `json:"included_team_ids"`
	IncludedChannelIDs   []string `json:"included_channel_ids"`
	ExcludedChannelIDs   []string `json:"excluded_channel_ids"`
	IncludePublicChannel bool     `json:"include_public_channel"`
	IncludePrivateChan   bool     `json:"include_private_channel"`
	IncludeDirectChan    bool     `json:"include_direct_channel"`
	IncludeGroupChan     bool     `json:"include_group_channel"`
	IncludeThreads       bool     `json:"include_threads"`
	ExcludeBotMessages   bool     `json:"exclude_bot_messages"`
	ExcludeSystemMessage bool     `json:"exclude_system_messages"`
}

type AnalysisOperations struct {
	RetentionDays      int  `json:"retention_days"`
	HotTopicLimit      int  `json:"hot_topic_limit"`
	ReportKeywordLimit int  `json:"report_keyword_limit"`
	AlertWindowHours   int  `json:"alert_window_hours"`
	AlertSpikeThresh   int  `json:"alert_spike_threshold"`
	AnonymizeAuthors   bool `json:"anonymize_authors"`
	ReindexBatchSize   int  `json:"reindex_batch_size"`
}

type DictionaryEntry struct {
	ID            string   `json:"id"`
	MajorCategory string   `json:"major_category"`
	SubCategory   string   `json:"sub_category"`
	Purpose       string   `json:"purpose"`
	Keywords      []string `json:"keywords"`
	Enabled       bool     `json:"enabled"`
}

type AlertRule struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	MajorCategory string `json:"major_category"`
	SubCategory   string `json:"sub_category"`
	Keyword       string `json:"keyword"`
	Threshold     int    `json:"threshold"`
	WindowHours   int    `json:"window_hours"`
	Enabled       bool   `json:"enabled"`
}

type AISettings struct {
	VLLMURL   string `json:"vllm_url"`
	VLLMKey   string `json:"vllm_key"`
	VLLMModel string `json:"vllm_model"`
}

type storedPluginConfig struct {
	Scope        AnalysisScope      `json:"scope"`
	Operations   AnalysisOperations `json:"operations"`
	Dictionaries []DictionaryEntry  `json:"dictionaries"`
	Stopwords    []string           `json:"stopwords"`
	AlertRules   []AlertRule        `json:"alert_rules"`
	AI           AISettings         `json:"ai"`
}

type compiledKeyword struct {
	Keyword       string
	Normalized    string
	MajorCategory string
	SubCategory   string
	Purpose       string
}

type runtimeConfiguration struct {
	Scope          AnalysisScope
	Operations     AnalysisOperations
	Dictionaries   []DictionaryEntry
	Stopwords      map[string]struct{}
	AlertRules     []AlertRule
	AI             AISettings
	CompiledLookup []compiledKeyword
}

func defaultStoredPluginConfig() storedPluginConfig {
	return storedPluginConfig{
		Scope: AnalysisScope{
			IncludePublicChannel: true,
			IncludePrivateChan:   true,
			IncludeDirectChan:    false,
			IncludeGroupChan:     false,
			IncludeThreads:       true,
			ExcludeBotMessages:   true,
			ExcludeSystemMessage: true,
		},
		Operations: AnalysisOperations{
			RetentionDays:      defaultRetentionDays,
			HotTopicLimit:      defaultHotTopicLimit,
			ReportKeywordLimit: defaultReportKeywordLimit,
			AlertWindowHours:   defaultAlertWindowHours,
			AlertSpikeThresh:   defaultAlertSpikeThreshold,
			AnonymizeAuthors:   false,
			ReindexBatchSize:   defaultReindexBatchSize,
		},
		Dictionaries: defaultDictionaryEntries(),
		Stopwords:    defaultStopwords(),
		AlertRules:   defaultAlertRules(),
	}
}

func (c storedPluginConfig) adminEditableConfig() storedPluginConfig {
	c.Dictionaries = nil
	c.Stopwords = nil
	return c
}

func parseStoredPluginConfig(raw string) (storedPluginConfig, error) {
	cfg := defaultStoredPluginConfig()
	if strings.TrimSpace(raw) == "" {
		return cfg, nil
	}

	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return storedPluginConfig{}, fmt.Errorf("invalid Config JSON: %w", err)
	}

	return cfg, nil
}

func (c *configuration) getStoredPluginConfig() (storedPluginConfig, string, error) {
	stored, err := parseStoredPluginConfig(c.Config)
	if err != nil {
		return storedPluginConfig{}, "config", err
	}
	return stored, "config", nil
}

func (c *configuration) normalize() (*runtimeConfiguration, error) {
	stored, _, err := c.getStoredPluginConfig()
	if err != nil {
		return nil, err
	}
	return stored.normalize()
}

func (c storedPluginConfig) normalize() (*runtimeConfiguration, error) {
	dictionaries := normalizeDictionaryEntries(c.Dictionaries)
	if len(dictionaries) == 0 {
		dictionaries = normalizeDictionaryEntries(defaultDictionaryEntries())
	}

	alertRules := normalizeAlertRules(c.AlertRules)
	if len(alertRules) == 0 {
		alertRules = normalizeAlertRules(defaultAlertRules())
	}

	stopwords := normalizeStopwords(c.Stopwords)
	if len(stopwords) == 0 {
		stopwords = normalizeStopwords(defaultStopwords())
	}

	cfg := &runtimeConfiguration{
		Scope: AnalysisScope{
			IncludedTeamIDs:      normalizeIdentifierSlice(c.Scope.IncludedTeamIDs),
			IncludedChannelIDs:   normalizeIdentifierSlice(c.Scope.IncludedChannelIDs),
			ExcludedChannelIDs:   normalizeIdentifierSlice(c.Scope.ExcludedChannelIDs),
			IncludePublicChannel: c.Scope.IncludePublicChannel,
			IncludePrivateChan:   c.Scope.IncludePrivateChan,
			IncludeDirectChan:    c.Scope.IncludeDirectChan,
			IncludeGroupChan:     c.Scope.IncludeGroupChan,
			IncludeThreads:       c.Scope.IncludeThreads,
			ExcludeBotMessages:   c.Scope.ExcludeBotMessages,
			ExcludeSystemMessage: c.Scope.ExcludeSystemMessage,
		},
		Operations: AnalysisOperations{
			RetentionDays:      positiveOrDefault(c.Operations.RetentionDays, defaultRetentionDays),
			HotTopicLimit:      positiveOrDefault(c.Operations.HotTopicLimit, defaultHotTopicLimit),
			ReportKeywordLimit: positiveOrDefault(c.Operations.ReportKeywordLimit, defaultReportKeywordLimit),
			AlertWindowHours:   positiveOrDefault(c.Operations.AlertWindowHours, defaultAlertWindowHours),
			AlertSpikeThresh:   positiveOrDefault(c.Operations.AlertSpikeThresh, defaultAlertSpikeThreshold),
			AnonymizeAuthors:   c.Operations.AnonymizeAuthors,
			ReindexBatchSize:   positiveOrDefault(c.Operations.ReindexBatchSize, defaultReindexBatchSize),
		},
		Dictionaries: dictionaries,
		Stopwords:    sliceToSet(stopwords),
		AlertRules:   alertRules,
		AI: AISettings{
			VLLMURL:   strings.TrimSpace(c.AI.VLLMURL),
			VLLMKey:   strings.TrimSpace(c.AI.VLLMKey),
			VLLMModel: strings.TrimSpace(c.AI.VLLMModel),
		},
	}
	cfg.CompiledLookup = compileKeywordLookup(cfg.Dictionaries)
	return cfg, nil
}

func normalizeDictionaryEntries(entries []DictionaryEntry) []DictionaryEntry {
	normalized := make([]DictionaryEntry, 0, len(entries))
	seen := map[string]struct{}{}

	for index, entry := range entries {
		major := strings.TrimSpace(entry.MajorCategory)
		sub := strings.TrimSpace(entry.SubCategory)
		purpose := strings.TrimSpace(entry.Purpose)
		keywords := normalizeKeywordList(entry.Keywords)
		if major == "" || sub == "" || len(keywords) == 0 {
			continue
		}

		entryID := strings.TrimSpace(entry.ID)
		if entryID == "" {
			entryID = buildDictionaryEntryID(index, major, sub)
		}
		if _, ok := seen[entryID]; ok {
			continue
		}
		seen[entryID] = struct{}{}

		normalized = append(normalized, DictionaryEntry{
			ID:            entryID,
			MajorCategory: major,
			SubCategory:   sub,
			Purpose:       purpose,
			Keywords:      keywords,
			Enabled:       entry.Enabled,
		})
	}

	return normalized
}

func normalizeAlertRules(rules []AlertRule) []AlertRule {
	normalized := make([]AlertRule, 0, len(rules))
	seen := map[string]struct{}{}

	for index, rule := range rules {
		name := strings.TrimSpace(rule.Name)
		keyword := strings.TrimSpace(rule.Keyword)
		major := strings.TrimSpace(rule.MajorCategory)
		sub := strings.TrimSpace(rule.SubCategory)
		if name == "" {
			if keyword != "" {
				name = keyword + " 급증"
			} else if sub != "" {
				name = sub + " 급증"
			} else if major != "" {
				name = major + " 급증"
			} else {
				continue
			}
		}

		id := strings.TrimSpace(rule.ID)
		if id == "" {
			id = fmt.Sprintf("alert-%d-%s", index+1, sanitizeConfigName(name))
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}

		normalized = append(normalized, AlertRule{
			ID:            id,
			Name:          name,
			MajorCategory: major,
			SubCategory:   sub,
			Keyword:       keyword,
			Threshold:     positiveOrDefault(rule.Threshold, defaultAlertSpikeThreshold),
			WindowHours:   positiveOrDefault(rule.WindowHours, defaultAlertWindowHours),
			Enabled:       rule.Enabled,
		})
	}

	return normalized
}

func normalizeIdentifierSlice(values []string) []string {
	normalized := make([]string, 0, len(values))
	seen := map[string]struct{}{}

	for _, value := range values {
		item := strings.TrimSpace(value)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}

	return normalized
}

func normalizeKeywordList(values []string) []string {
	normalized := make([]string, 0, len(values))
	seen := map[string]struct{}{}

	for _, value := range values {
		item := strings.TrimSpace(value)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, item)
	}

	return normalized
}

func normalizeStopwords(values []string) []string {
	normalized := make([]string, 0, len(values))
	seen := map[string]struct{}{}

	for _, value := range values {
		item := strings.ToLower(strings.TrimSpace(value))
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
	}

	return normalized
}

func sliceToSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func compileKeywordLookup(entries []DictionaryEntry) []compiledKeyword {
	lookup := make([]compiledKeyword, 0, len(entries)*2)
	for _, entry := range entries {
		if !entry.Enabled {
			continue
		}
		for _, keyword := range entry.Keywords {
			normalized := normalizeKeywordKey(keyword)
			if normalized == "" {
				continue
			}
			lookup = append(lookup, compiledKeyword{
				Keyword:       keyword,
				Normalized:    normalized,
				MajorCategory: entry.MajorCategory,
				SubCategory:   entry.SubCategory,
				Purpose:       entry.Purpose,
			})
		}
	}
	return lookup
}

func defaultStopwords() []string {
	return []string{
		"그리고", "그런데", "그래서", "지금", "이번", "저희", "관련", "확인", "문의", "요청", "처리",
		"이슈", "진행", "가능", "필요", "현재", "해당", "부분", "일단", "정도", "because", "please",
		"thanks", "thank", "hello", "there", "with", "have", "from", "into", "that", "this", "will",
	}
}

func defaultAlertRules() []AlertRule {
	return []AlertRule{
		{
			ID:            "alert-outage-spike",
			Name:          "장애 키워드 급증",
			MajorCategory: "장애",
			Threshold:     5,
			WindowHours:   24,
			Enabled:       true,
		},
		{
			ID:            "alert-security-signal",
			Name:          "보안 징후 감지",
			MajorCategory: "보안",
			Threshold:     3,
			WindowHours:   24,
			Enabled:       true,
		},
		{
			ID:            "alert-release-failure",
			Name:          "배포 실패 감지",
			MajorCategory: "배포",
			SubCategory:   "배포 실패",
			Threshold:     2,
			WindowHours:   12,
			Enabled:       true,
		},
	}
}

func defaultDictionaryEntries() []DictionaryEntry {
	raw := []struct {
		major    string
		sub      string
		purpose  string
		keywords []string
	}{
		{"장애", "시스템 장애", "장애 탐지", []string{"다운", "죽음", "응답없음", "hang", "먹통", "서비스 중단", "장애", "서비스 장애", "죽었다", "503", "502"}},
		{"장애", "성능 장애", "성능 분석", []string{"느림", "지연", "latency", "timeout", "응답 지연", "속도 저하", "병목", "슬로우", "처리 지연", "느려짐"}},
		{"장애", "간헐적 오류", "반복 장애 탐지", []string{"간헐적", "재현불가", "intermittent", "가끔", "간헐 오류", "왔다갔다", "간헐 발생", "불규칙", "일시적"}},
		{"장애", "장애 전조", "사전 감지", []string{"이상징후", "warning", "이상", "전조", "조짐", "불안정", "warn", "비정상", "오류 징후"}},
		{"배포", "릴리즈", "배포 추적", []string{"배포", "릴리즈", "release", "deploy", "반영", "배포 완료", "배포 일정", "릴리즈 노트"}},
		{"배포", "롤백", "실패 대응", []string{"rollback", "되돌림", "롤백", "원복", "배포 되돌림", "원복 처리"}},
		{"배포", "배포 실패", "배포 품질", []string{"배포 실패", "실패", "에러", "실패율", "deploy fail", "릴리즈 실패", "반영 실패", "배포 오류", "실패 건수"}},
		{"배포", "긴급 패치", "긴급 대응", []string{"hotfix", "긴급 수정", "긴급 패치", "패치", "즉시 배포", "긴급 반영"}},
		{"인프라", "서버", "자원 관리", []string{"서버", "instance", "VM", "vm", "host", "머신", "노드 서버", "서버 자원"}},
		{"인프라", "컨테이너", "클라우드 운영", []string{"docker", "pod", "container", "컨테이너", "이미지", "image", "sidecar"}},
		{"인프라", "쿠버네티스", "오케스트레이션", []string{"k8s", "node", "cluster", "쿠버", "쿠버네티스", "daemonset", "deployment", "statefulset"}},
		{"인프라", "스토리지", "저장소 이슈", []string{"disk", "volume", "NAS", "nas", "storage", "스토리지", "디스크", "마운트", "용량 부족"}},
		{"네트워크", "연결 문제", "장애 원인", []string{"연결 안됨", "unreachable", "접속 안됨", "connection reset", "refused", "네트워크 오류", "연결 실패", "끊김"}},
		{"네트워크", "DNS", "네임 해석", []string{"DNS", "dns", "domain", "도메인", "name server", "resolve", "해석 실패"}},
		{"네트워크", "방화벽", "보안/네트워크", []string{"firewall", "차단", "보안 그룹", "security group", "포트 차단", "allowlist", "deny"}},
		{"네트워크", "트래픽", "부하 분석", []string{"트래픽", "bandwidth", "throughput", "패킷", "대역폭", "부하", "traffic spike"}},
		{"데이터베이스", "쿼리", "성능", []string{"query", "select", "join", "쿼리", "full scan", "slow query", "실행 계획", "인덱스"}},
		{"데이터베이스", "락", "병목 분석", []string{"lock", "deadlock", "락", "교착상태", "wait", "row lock", "table lock"}},
		{"데이터베이스", "복제", "데이터 안정성", []string{"replication", "sync", "복제", "싱크", "replica", "lag", "slave", "standby"}},
		{"데이터베이스", "커넥션", "DB 연결", []string{"connection", "pool", "DB 접속 오류", "db 접속 오류", "커넥션", "pool exhausted", "too many connections", "연결 수 초과"}},
		{"보안", "인증", "인증 문제", []string{"로그인", "인증", "SSO", "sso", "login", "oauth", "토큰 만료", "인증 실패", "재로그인"}},
		{"보안", "권한", "접근 제어", []string{"권한", "role", "access", "permission", "인가", "접근 제어", "권한 부족", "접근 불가"}},
		{"보안", "침해", "보안 사고", []string{"해킹", "공격", "exploit", "침해", "악성", "침투", "bruteforce", "랜섬웨어"}},
		{"보안", "취약점", "리스크 관리", []string{"취약점", "CVE", "cve", "보안 결함", "패치 필요", "vulnerability", "위험도"}},
		{"개발", "코드", "개발 활동", []string{"코드", "함수", "클래스", "모듈", "패키지", "리팩토링", "구현", "소스"}},
		{"개발", "버그", "품질 관리", []string{"버그", "오류", "fix", "수정", "예외", "exception", "결함", "이상 동작"}},
		{"개발", "리뷰", "협업", []string{"PR", "pr", "리뷰", "approve", "merge request", "코드리뷰", "comment", "리뷰 요청"}},
		{"개발", "테스트", "품질", []string{"테스트", "unit", "e2e", "integration test", "qa", "테스트케이스", "회귀 테스트"}},
		{"CI/CD", "빌드", "파이프라인", []string{"build", "compile", "빌드", "artifact", "패키징", "번들", "assemble"}},
		{"CI/CD", "파이프라인", "자동화", []string{"pipeline", "workflow", "stage", "job", "runner", "action", "workflow run"}},
		{"CI/CD", "실패", "안정성", []string{"실패", "broken", "빌드 실패", "job fail", "pipeline fail", "실패 로그", "red build"}},
		{"CI/CD", "배포 자동화", "효율성", []string{"자동배포", "CD", "cd", "continuous delivery", "자동 반영", "promotion"}},
		{"모니터링", "로그", "분석", []string{"log", "로그", "에러 로그", "로그 조회", "로그 수집", "stacktrace", "trace log"}},
		{"모니터링", "메트릭", "성능", []string{"metric", "CPU", "cpu", "memory", "메트릭", "사용률", "utilization", "heap", "load average"}},
		{"모니터링", "알람", "대응", []string{"alert", "alarm", "알람", "경보", "알림", "trigger", "임계치 초과", "pager"}},
		{"모니터링", "트레이싱", "분산 추적", []string{"tracing", "span", "trace", "distributed tracing", "트레이싱", "trace id"}},
		{"클라우드", "AWS", "클라우드 사용", []string{"EC2", "ec2", "S3", "s3", "RDS", "rds", "iam", "elb", "lambda", "ecs"}},
		{"클라우드", "GCP", "플랫폼", []string{"GKE", "gke", "BigQuery", "bigquery", "gcs", "cloud run", "pubsub", "gcp"}},
		{"클라우드", "Azure", "멀티클라우드", []string{"VM", "vm", "Blob", "blob", "aks", "azure", "app service", "azure sql"}},
		{"클라우드", "SaaS", "의존성", []string{"API", "api", "외부서비스", "third-party", "saas", "외부 api", "vendor", "연동 서비스"}},
		{"계정", "생성", "요청 분석", []string{"계정 생성", "신규 계정", "온보딩 계정", "계정 발급", "아이디 생성"}},
		{"계정", "삭제", "관리", []string{"계정 삭제", "계정 제거", "퇴사자 계정", "계정 비활성화", "탈퇴 처리"}},
		{"계정", "잠금", "장애", []string{"계정 잠금", "잠김", "lockout", "잠금 해제", "로그인 잠금", "계정 정지"}},
		{"계정", "권한 요청", "운영", []string{"권한 요청", "권한 부여", "role 요청", "접근 권한", "권한 변경"}},
		{"운영요청", "설정 변경", "요청 유형", []string{"설정 변경", "설정 수정", "파라미터 변경", "환경 변경", "옵션 변경"}},
		{"운영요청", "접근 요청", "승인", []string{"접근 요청", "접속 요청", "승인 요청", "방화벽 오픈", "접근 허용"}},
		{"운영요청", "데이터 요청", "지원", []string{"데이터 요청", "데이터 추출", "조회 요청", "엑셀 요청", "리포트 요청"}},
		{"운영요청", "문의", "FAQ", []string{"문의", "질문", "헬프", "help", "사용 방법", "어떻게"}},
		{"운영요청", "공지", "운영 커뮤니케이션", []string{"공지", "안내", "notice", "공유 공지", "전파", "운영 공지"}},
		{"운영요청", "점검", "변경 관리", []string{"점검", "정기 점검", "maintenance", "점검 예정", "점검 시간", "서비스 점검"}},
		{"신용평가", "등급 액션", "평가 의사결정", []string{"신용등급", "등급", "아웃룩", "outlook", "watch", "등급 상향", "등급 하향", "등급 유지", "등급 액션", "notch"}},
		{"신용평가", "기업 분석", "기업 신용평가", []string{"사업위험", "재무위험", "산업전망", "peer", "벤치마크", "실적 전망", "수익성", "현금흐름", "차입금", "레버리지", "ebitda", "ffo"}},
		{"신용평가", "위원회", "평가 프로세스", []string{"등급위원회", "위원회", "부의", "의안", "심의", "결의", "회의록", "committee", "검토 의견", "코멘트 반영"}},
		{"회원사 대응", "평가 문의", "회원사 커뮤니케이션", []string{"회원사", "회원사 문의", "발행사", "실무자", "담당자", "평가 문의", "등급 문의", "아웃룩 문의", "평가 결과 문의", "평가 의견"}},
		{"회원사 대응", "자료 제출", "평가 자료 수집", []string{"자료 제출", "제출 자료", "보완자료", "추가 자료", "재무자료", "재무제표", "사업계획", "자료 송부", "자료 전달", "자료 업로드", "제출 기한"}},
		{"회원사 대응", "일정 조율", "평가 일정 관리", []string{"평가 일정", "미팅 일정", "인터뷰 일정", "실사 일정", "방문 일정", "위원회 일정", "일정 조율", "일정 변경", "스케줄 조정", "회의 요청"}},
		{"회원사 대응", "보고서 배포", "결과 전달", []string{"평가서", "보고서 송부", "보고서 발송", "보고서 전달", "결과 공유", "등급 통보", "의견서", "레터", "송부 요청", "배포 대상"}},
		{"회원사 대응", "시스템 이용", "포털 지원", []string{"회원사 포털", "포털 접속", "로그인 오류", "아이디 발급", "비밀번호 초기화", "권한 부여", "열람 권한", "다운로드 권한", "계정 문의", "사용자 등록"}},
		{"회원사 대응", "계약 및 수수료", "계약 운영", []string{"계약서", "약정서", "수수료", "평가 수수료", "세금계산서", "청구서", "견적", "정산", "계약 갱신", "계약 검토"}},
		{"회원사 대응", "정정 및 이의", "품질 대응", []string{"정정 요청", "오탈자", "수정 요청", "재검토", "재심", "이의제기", "이견", "코멘트 반영", "팩트체크", "문안 수정"}},
		{"채권시장", "발행 및 차환", "시장 모니터링", []string{"회사채", "여전채", "cp", "CP", "전단채", "사모채", "공모채", "발행", "차환", "만기", "수요예측", "스프레드"}},
		{"구조화금융", "유동화", "구조화금융 평가", []string{"ABS", "abs", "ABCP", "abcp", "MBS", "mbs", "유동화", "트랜치", "credit enhancement", "신용보강", "SPC", "true sale"}},
		{"구조화금융", "부동산 PF", "PF 리스크", []string{"PF", "pf", "부동산pf", "브릿지론", "본pf", "착공", "준공", "분양률", "LTV", "ltv", "DSCR", "dscr"}},
		{"규제", "공시 및 회계", "규제 대응", []string{"공시", "사업보고서", "분기보고서", "반기보고서", "감사보고서", "IFRS", "ifrs", "정정공시", "주석", "회계 이슈"}},
		{"규제", "감독 대응", "감독기관 대응", []string{"금감원", "금융위", "감독규정", "내부통제", "스트레스 테스트", "바젤", "capital adequacy", "건전성", "리스크 한도", "감독 이슈"}},
		{"성능", "CPU", "자원", []string{"CPU", "CPU 사용량", "cpu 사용량", "cpu spike", "load", "프로세서 사용량"}},
		{"성능", "메모리", "성능", []string{"메모리 부족", "memory leak", "oom", "heap", "메모리 사용량", "메모리 초과"}},
		{"성능", "I/O", "병목", []string{"disk IO", "disk io", "iops", "read latency", "write latency", "io wait"}},
		{"성능", "용량", "확장", []string{"capacity", "용량", "한계", "스케일 업", "스케일 아웃", "증설"}},
		{"협업", "공유", "협업", []string{"공유", "전달", "share", "공유드립니다", "전파", "참고 부탁"}},
		{"협업", "회의", "일정", []string{"회의", "미팅", "meeting", "sync up", "콜", "회의실", "일정 조율"}},
		{"협업", "결정", "의사결정", []string{"결정", "합의", "승인", "의사결정", "결론", "확정"}},
		{"협업", "요청", "흐름", []string{"부탁", "요청", "request", "지원 부탁", "확인 부탁", "처리 부탁"}},
		{"대응", "긴급 대응", "우선순위", []string{"urgent", "ASAP", "asap", "긴급", "최우선", "바로", "즉시", "우선 대응"}},
		{"대응", "RCA", "원인 분석", []string{"root cause", "RCA", "rca", "원인 분석", "근본 원인", "원인 후보"}},
		{"대응", "조치", "처리", []string{"조치 완료", "조치", "처리 완료", "적용 완료", "반영 완료", "대응 완료"}},
		{"대응", "복구", "안정화", []string{"복구 완료", "복구", "정상화", "서비스 복구", "안정화", "회복"}},
		{"데이터", "ETL", "데이터 흐름", []string{"ETL", "etl", "파이프라인", "적재", "수집", "변환", "배치 적재"}},
		{"데이터", "분석", "BI", []string{"분석", "리포트", "대시보드", "통계", "지표", "BI", "보고서"}},
		{"데이터", "모델", "AI", []string{"모델", "학습", "training", "튜닝", "feature", "ml model"}},
		{"데이터", "추론", "서비스", []string{"inference", "추론", "serving", "응답 생성", "실시간 추론"}},
		{"AI", "LLM 개발", "생성형 AI 개발", []string{"llm", "gpt", "생성형 ai", "genai", "foundation model", "instruction tuning", "sft", "fine tuning", "파인튜닝", "모델 평가"}},
		{"AI", "프롬프트", "프롬프트 엔지니어링", []string{"prompt", "프롬프트", "system prompt", "user prompt", "prompt template", "few-shot", "zero-shot", "prompt injection", "프롬프트 엔지니어링"}},
		{"AI", "RAG", "지식 검색", []string{"rag", "retrieval augmented generation", "embedding", "embeddings", "vector db", "vector store", "semantic search", "rerank", "reranker", "chunking", "청킹", "하이브리드 검색"}},
		{"AI", "서빙 및 운영", "AI 운영", []string{"vllm", "tgi", "text generation inference", "model serving", "serving gateway", "inference server", "online inference", "batch inference", "serving latency", "토큰 사용량"}},
		{"AI", "MLOps", "모델 운영 자동화", []string{"mlops", "model registry", "feature store", "drift", "data drift", "model drift", "hallucination", "guardrail", "eval", "evaluation", "observability", "모델 모니터링"}},
		{"AI", "에이전트", "AI 자동화", []string{"agent", "ai agent", "tool calling", "function calling", "planner", "reasoning", "multi-agent", "workflow agent", "agent loop"}},
		{"AI", "GPU 운영", "가속기 인프라", []string{"gpu", "cuda", "vram", "tensor rt", "tensorrt", "nvidia", "a100", "h100", "gpu memory", "gpu oom", "accelerator"}},
		{"보안운영", "감사", "규정", []string{"audit", "감사 로그", "audit log", "compliance", "추적", "감사 추적", "감사 대응"}},
		{"보안운영", "정책", "통제", []string{"policy", "정책", "보안 정책", "가이드라인", "통제", "차단 정책"}},
		{"보안운영", "인증서", "보안", []string{"cert", "SSL", "ssl", "TLS", "tls", "인증서 만료", "certificate"}},
		{"보안운영", "키 관리", "민감정보", []string{"key", "secret", "비밀키", "암호키", "credential", "vault"}},
		{"DevOps", "IaC", "자동화", []string{"terraform", "ansible", "iac", "infrastructure as code", "helm", "module"}},
		{"DevOps", "스크립트", "운영", []string{"script", "cron", "shell", "powershell", "bash", "스크립트", "작업 스크립트"}},
		{"DevOps", "자동화", "효율", []string{"automation", "자동화", "자동 처리", "오케스트레이션", "self-service"}},
		{"DevOps", "배치", "작업", []string{"batch", "배치", "scheduler", "스케줄러", "job", "정기 작업"}},
	}

	entries := make([]DictionaryEntry, 0, len(raw))
	for index, item := range raw {
		entries = append(entries, DictionaryEntry{
			ID:            buildDictionaryEntryID(index, item.major, item.sub),
			MajorCategory: item.major,
			SubCategory:   item.sub,
			Purpose:       item.purpose,
			Keywords:      item.keywords,
			Enabled:       true,
		})
	}

	return entries
}

func buildDictionaryEntryID(index int, major, sub string) string {
	return fmt.Sprintf("dict-%02d-%s-%s", index+1, sanitizeConfigName(major), sanitizeConfigName(sub))
}

func sanitizeConfigName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "/", "-")
	value = strings.ReplaceAll(value, "_", "-")
	return value
}

func positiveOrDefault(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	if p.configuration == nil {
		return &configuration{}
	}

	return p.configuration
}

func (p *Plugin) getRuntimeConfiguration() (*runtimeConfiguration, error) {
	return p.getConfiguration().normalize()
}

func (p *Plugin) setConfiguration(cfg *configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()

	p.configuration = cfg
}

func (p *Plugin) OnConfigurationChange() error {
	cfg := new(configuration)
	if err := p.API.LoadPluginConfiguration(cfg); err != nil {
		return fmt.Errorf("failed to load plugin configuration: %w", err)
	}

	p.setConfiguration(cfg)
	return nil
}

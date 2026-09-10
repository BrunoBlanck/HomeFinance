export const meta = {
  name: 'auditoria-seguranca',
  description: 'Auditoria profunda de segurança do HomeFinance: revisores paralelos por dimensão + verificação adversarial de cada achado',
  whenToUse: 'Quando o usuário pedir uma auditoria de segurança completa do projeto (mais profunda que a skill /revisao-seguranca)',
  phases: [
    { title: 'Revisar', detail: 'um revisor por dimensão de segurança, em paralelo' },
    { title: 'Verificar', detail: 'cada achado é verificado adversarialmente' },
  ],
}

// Dimensões alinhadas ao checklist de docs/SEGURANCA.md
const DIMENSOES = [
  { key: 'autorizacao', prompt: 'Audite AUTORIZAÇÃO no projeto HomeFinance (leia docs/SEGURANCA.md seção 2 antes). Procure: queries sem filtro por household_id, IDs de recurso vindos do cliente usados sem checar posse (IDOR), checagem de papel ausente, 403 onde deveria ser 404. Examine backend/ inteiro.' },
  { key: 'injecao', prompt: 'Audite INJEÇÃO no projeto HomeFinance (docs/SEGURANCA.md seção 3). Procure: SQL concatenado (inclusive ORDER BY/LIMIT/colunas dinâmicas sem allowlist), entrada em comandos/caminhos, JSON decoder sem limites. Examine backend/ inteiro.' },
  { key: 'autenticacao', prompt: 'Audite AUTENTICAÇÃO E SESSÃO no HomeFinance (docs/SEGURANCA.md seção 1). Procure: hashing fraco ou parâmetros baixos de Argon2id, JWT sem validar alg/exp/iss, refresh sem rotação/detecção de reuso, tokens em localStorage, falta de rate limit em login/refresh, comparações não constantes.' },
  { key: 'vazamento', prompt: 'Audite VAZAMENTO DE INFORMAÇÃO no HomeFinance (docs/SEGURANCA.md seções 4 e 7). Procure: erros internos expostos ao cliente, dados sensíveis em logs (senha/token/corpo), entidades do banco serializadas direto na resposta, segredos hardcoded ou em arquivos versionados.' },
  { key: 'frontend-config', prompt: 'Audite FRONTEND E CONFIGURAÇÃO no HomeFinance (docs/SEGURANCA.md seções 5, 6 e 8). Procure: XSS (dangerouslySetInnerHTML, URLs concatenadas), dados sensíveis em query string, CORS permissivo, headers de segurança ausentes, dependências vulneráveis (rode govulncheck e npm audit se os projetos existirem e reporte a saída real).' },
]

const ACHADOS_SCHEMA = {
  type: 'object',
  required: ['findings'],
  properties: {
    findings: {
      type: 'array',
      items: {
        type: 'object',
        required: ['titulo', 'arquivo', 'severidade', 'cenario'],
        properties: {
          titulo: { type: 'string' },
          arquivo: { type: 'string', description: 'caminho:linha' },
          severidade: { enum: ['critica', 'alta', 'media', 'baixa'] },
          cenario: { type: 'string', description: 'entrada X leva à consequência Y' },
          correcao: { type: 'string' },
        },
      },
    },
  },
}

const VEREDITO_SCHEMA = {
  type: 'object',
  required: ['real', 'justificativa'],
  properties: {
    real: { type: 'boolean' },
    justificativa: { type: 'string' },
    severidadeAjustada: { enum: ['critica', 'alta', 'media', 'baixa'] },
  },
}

const resultados = await pipeline(
  DIMENSOES,
  (d) =>
    agent(
      `${d.prompt}\n\nResponda em português. Só reporte achados confirmados no código com cenário concreto de exploração — nada especulativo. Se a área ainda não existe no repositório, retorne lista vazia.`,
      { label: `revisar:${d.key}`, phase: 'Revisar', schema: ACHADOS_SCHEMA, agentType: 'revisor-seguranca' },
    ),
  (revisao, dim) =>
    parallel(
      (revisao?.findings ?? []).map((f) => () =>
        agent(
          `Verifique adversarialmente este achado de segurança no HomeFinance e tente REFUTÁ-LO lendo o código real em ${f.arquivo}:\n\nTítulo: ${f.titulo}\nCenário alegado: ${f.cenario}\n\nO achado só é real se o cenário de exploração funciona de fato no código como está. Responda em português.`,
          { label: `verificar:${f.titulo.slice(0, 40)}`, phase: 'Verificar', schema: VEREDITO_SCHEMA },
        ).then((v) => ({ ...f, dimensao: dim.key, veredito: v })),
      ),
    ),
)

const confirmados = resultados
  .filter(Boolean)
  .flat()
  .filter(Boolean)
  .filter((f) => f.veredito?.real)
  .map((f) => ({ ...f, severidade: f.veredito.severidadeAjustada ?? f.severidade }))

const ordem = { critica: 0, alta: 1, media: 2, baixa: 3 }
confirmados.sort((a, b) => ordem[a.severidade] - ordem[b.severidade])

log(`Auditoria concluída: ${confirmados.length} achado(s) confirmado(s)`)
return {
  veredito: confirmados.some((f) => f.severidade === 'critica' || f.severidade === 'alta') ? 'BLOQUEADO' : 'APROVADO',
  achados: confirmados,
}

import { useMutation, useQuery } from '@tanstack/react-query'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import { ApiError } from '@/api/client'
import type { Account, Institution } from '@/api/types'
import { AccountDialog, type AccountPrefill } from '@/components/AccountDialog/AccountDialog'
import { Alert } from '@/components/Alert/Alert'
import { FlashAlert } from '@/components/Alert/FlashAlert'
import { FormError } from '@/components/Alert/FormError'
import { Button } from '@/components/Button/Button'
import { FileField } from '@/components/FileField/FileField'
import { PlusIcon } from '@/components/icons/PlusIcon'
import { Panel } from '@/components/Panel/Panel'
import { PasswordField } from '@/components/PasswordField/PasswordField'
import { Select } from '@/components/Select/Select'
import {
  contasQueryOptions,
  INSTITUICOES,
  opcoesDeConta,
  ROTULO_DA_INSTITUICAO,
} from '@/lib/accounts'
import { useFocoNoTitulo } from '@/lib/focus'
import { FUSO_PADRAO, hojeNoFuso } from '@/lib/month'
import { sessionQueryOptions } from '@/lib/session'
import { criarImportacao } from '../api/imports'
import { ImportStepper } from '../components/ImportStepper'
import styles from './ImportUploadScreen.module.css'

/** Extensões que o servidor sabe ler. Filtro de conveniência do seletor do
 *  sistema, **nunca** validação: quem decide o que o arquivo é são os *magic
 *  bytes*, no backend. Renomear um `.exe` para `.csv` engana este atributo e
 *  não engana o servidor. */
const EXTENSOES = '.csv,.zip'

const FALHA_DE_ENVIO = 'Não foi possível enviar o arquivo. Verifique sua conexão e tente de novo.'

type ErrosDoPasso1 = {
  conta?: string
  arquivo?: string
  senha?: string
  geral?: string
}

/** O que o `IMPORT_TARGET_MISMATCH` virou: não um beco ("não é desta conta"),
 *  mas um caminho. O servidor diz por que não bateu (`fields.reason`) e a tela
 *  oferece a saída — escolher ou criar a conta certa. */
type Recuperacao =
  | { tipo: 'expected_credit_card'; instituicao: Institution | null }
  | { tipo: 'expected_bank_account'; instituicao: Institution | null }
  | { tipo: 'wrong_institution'; detectada: Institution | null; daConta: Institution | null }

/** Passo 1 da importação — enviar o arquivo.
 *
 *  Nada é gravado aqui: esta tela manda o arquivo para ANÁLISE, e o resultado
 *  vai para um *staging* que só vira lançamento depois que a pessoa aprovar no
 *  passo 2.
 *
 *  **A senha do arquivo é o CPF do titular.** Ela não vai para `sessionStorage`,
 *  não vai para `localStorage`, não vai para a query string, não entra em cache
 *  de query e sai do estado do React assim que a requisição parte. É o dado
 *  mais sensível que esta tela toca, e ele existe por segundos. */
export function ImportUploadScreen() {
  const navigate = useNavigate()
  // Foco no <h1> na entrada da rota (docs/DESIGN.md).
  const tituloRef = useFocoNoTitulo()
  const senhaRef = useRef<HTMLInputElement>(null)

  const [contaId, setContaId] = useState('')
  const [arquivo, setArquivo] = useState<File | null>(null)
  const [senha, setSenha] = useState('')
  const [pediuSenha, setPediuSenha] = useState(false)
  const [erros, setErros] = useState<ErrosDoPasso1>({})
  const [recuperacao, setRecuperacao] = useState<Recuperacao | null>(null)
  const [dialogoContaAberto, setDialogoContaAberto] = useState(false)
  const [prefillConta, setPrefillConta] = useState<AccountPrefill | undefined>(undefined)

  // Aviso trazido de outra tela — hoje, a revisão de um lote que expirou.
  const flash = useRouterState({ select: (estado) => estado.location.state.flash })

  const session = useQuery(sessionQueryOptions)
  const contas = useQuery(contasQueryOptions(false))

  // O `AccountDialog` precisa de "hoje" no fuso da CASA para o saldo de abertura;
  // quem sabe o fuso é a sessão (ADR-019).
  const hoje = hojeNoFuso(session.data?.household.timezone ?? FUSO_PADRAO)
  const contasItens = contas.data?.items ?? []

  useEffect(() => {
    document.title = 'Importar extrato · HomeFinance'
  }, [])

  // O foco vai para o campo no MESMO efeito que o revela: descobrir que
  // apareceu um campo novo no meio da página é trabalho de quem enxerga, e o
  // campo é exatamente o próximo passo.
  useEffect(() => {
    if (pediuSenha) senhaRef.current?.focus()
  }, [pediuSenha])

  const enviar = useMutation({
    mutationFn: () => {
      if (!arquivo) throw new Error('sem arquivo')
      const senhaDaVez = senha
      // Sai do estado antes do await: a partir daqui ela só existe dentro do
      // corpo da requisição, a caminho do servidor.
      setSenha('')
      return criarImportacao({
        file: arquivo,
        accountId: contaId,
        ...(senhaDaVez ? { password: senhaDaVez } : {}),
      })
    },
    onSuccess: (lote) => {
      void navigate({ to: '/importar/$importId/revisar', params: { importId: lote.id } })
    },
    onError: (error) => {
      const resultado = interpretarErro(error)
      setErros(resultado.erros)
      // `pediuSenha` é sticky: um erro que não seja de senha não pode esconder o
      // campo que um erro anterior revelou.
      if (resultado.pediuSenha) setPediuSenha(true)
      setRecuperacao(resultado.recuperacao)
    },
  })

  function submeter(evento: React.FormEvent) {
    evento.preventDefault()

    const encontrados: ErrosDoPasso1 = {}
    if (!contaId) encontrados.conta = 'Escolha a conta de destino.'
    if (!arquivo) encontrados.arquivo = 'Escolha o arquivo do extrato ou da fatura.'
    setErros(encontrados)
    if (encontrados.conta || encontrados.arquivo) return

    enviar.mutate()
  }

  /** Abre o diálogo de conta já preenchido a partir do motivo do erro — cartão
   *  de crédito para uma fatura, conta corrente para um extrato. */
  function criarConta() {
    if (!recuperacao) return
    setPrefillConta(prefillDaRecuperacao(recuperacao))
    setDialogoContaAberto(true)
  }

  // A senha aparece em dois casos, e só neles: o arquivo é um ZIP (todo ZIP do
  // C6 é cifrado), ou o servidor disse que precisa. Um campo de senha sempre
  // visível num formulário de upload pede um segredo que quase nunca existe.
  const mostrarSenha = ehZip(arquivo) || pediuSenha

  return (
    <div className={styles.pagina}>
      <div>
        <h1 className={styles.titulo} ref={tituloRef} tabIndex={-1}>
          Importar extrato ou fatura
        </h1>
        <p className={styles.apoio}>
          O arquivo é lido, conferido contra o que já existe e só entra depois que você aprovar.
        </p>
      </div>

      <ImportStepper atual="enviar" />

      {flash ? <FlashAlert flash={flash} /> : null}

      <form onSubmit={submeter} noValidate>
        <Panel
          title="Arquivo"
          footer={
            <p className={styles.rodape}>
              O arquivo fica no servidor só até você concluir ou cancelar esta importação.
            </p>
          }
        >
          <div className={styles.campos}>
            {recuperacao ? (
              <BlocoRecuperacao
                recuperacao={recuperacao}
                contas={contasItens}
                contaId={contaId}
                onEscolherConta={setContaId}
                onCriarConta={criarConta}
              />
            ) : null}

            {erros.geral ? <FormError id="importar-erro">{erros.geral}</FormError> : null}

            <Select
              label="Conta de destino"
              placeholder="Escolha a conta"
              hint="É a conta ou o cartão a que este arquivo pertence."
              options={opcoesDeConta(contasItens)}
              value={contaId}
              onChange={(evento) => setContaId(evento.target.value)}
              {...(erros.conta ? { error: erros.conta } : {})}
            />

            <FileField
              label="Arquivo do extrato ou da fatura"
              accept={EXTENSOES}
              hint="CSV do Nubank ou o ZIP do C6, do jeito que foi baixado — sem renomear nem abrir e salvar de novo."
              file={arquivo}
              onSelect={(escolhido) => {
                setArquivo(escolhido)
                // Trocar de arquivo zera o que o servidor tinha dito sobre o
                // anterior: manter "protegido por senha" de um ZIP depois de
                // escolher um CSV pediria um segredo que não existe mais, e a
                // recuperação de conta era sobre o outro arquivo.
                setPediuSenha(false)
                setSenha('')
                setErros({})
                setRecuperacao(null)
              }}
              {...(erros.arquivo ? { error: erros.arquivo } : {})}
            />

            {pediuSenha ? <Alert tone="info">Este arquivo está protegido por senha.</Alert> : null}

            {mostrarSenha ? (
              /* `autoComplete="off"` é OBRIGATÓRIO aqui, e é o único campo de
                 senha do app que o usa — o contrato manda (`password` de
                 `POST /imports`, em `backend/api/openapi.yaml`).

                 Esta senha é o **CPF do titular do cartão**: dado pessoal de um
                 terceiro, de uso único, que some do estado assim que a
                 requisição parte. Com `current-password`, o navegador ofereceria
                 salvá-lo como credencial da origem do HomeFinance e o
                 sincronizaria para o cofre do sistema ou da conta Google — um
                 lugar que esta aplicação não controla e de onde ela não pode
                 apagar. E como o login usa `current-password` na MESMA origem,
                 o navegador confundiria os dois campos e poderia sobrescrever a
                 senha da conta pelo CPF, preenchendo-o sozinho no login depois.

                 Quem "corrigir" isto de volta desfaz todo o cuidado do resto do
                 caminho, onde a senha vive como `[]byte` e é zerada em todos os
                 ramos. Ver `PasswordAutoComplete` no PasswordField. */
              <PasswordField
                ref={senhaRef}
                label="Senha do arquivo"
                autoComplete="off"
                hint="A senha do arquivo do C6 é o CPF do titular, só números, sem pontos nem traço. Ela serve só para abrir o arquivo agora e não é guardada em lugar nenhum."
                value={senha}
                onChange={(evento) => setSenha(evento.target.value)}
                {...(erros.senha ? { error: erros.senha } : {})}
              />
            ) : null}

            <div className={styles.acoes}>
              {/* Enquanto envia, o botão NUNCA recebe `disabled`: ele perderia
                  o foco e quem navega por teclado ficaria sem referência. */}
              <Button type="submit" variant="primary" loading={enviar.isPending}>
                Analisar arquivo
              </Button>
            </div>

            {enviar.isPending ? (
              <p className={styles.status} role="status">
                Enviando e lendo o arquivo. Isso leva alguns segundos.
              </p>
            ) : null}
          </div>
        </Panel>
      </form>

      <AccountDialog
        open={dialogoContaAberto}
        onClose={() => setDialogoContaAberto(false)}
        hoje={hoje}
        prefill={prefillConta}
        onCreated={(conta) => {
          // A conta nova entra no seletor (a query de contas foi invalidada) e
          // já fica pré-selecionada; o passo seguinte é reenviar o arquivo.
          setContaId(conta.id)
        }}
      />
    </div>
  )
}

/** Bloco de recuperação: a fatura foi para a conta errada, e aqui está a saída.
 *
 *  `Alert tone="warning"` porque é pendência de decisão humana (docs/DESIGN.md),
 *  com `autoFocus` para se anunciar — o foco vai para o aviso que acabou de
 *  aparecer, em vez de deixar a pessoa procurá-lo. */
function BlocoRecuperacao({
  recuperacao,
  contas,
  contaId,
  onEscolherConta,
  onCriarConta,
}: {
  recuperacao: Recuperacao
  contas: readonly Account[]
  contaId: string
  onEscolherConta: (id: string) => void
  onCriarConta: () => void
}) {
  const texto = textoDaRecuperacao(recuperacao)
  const sugeridas = contasSugeridas(recuperacao, contas)

  return (
    <Alert tone="warning" title={texto.titulo} autoFocus>
      <div className={styles.recuperacao}>
        <p>{texto.explicacao}</p>

        {sugeridas.length > 0 ? (
          <Select
            label={texto.rotuloEscolha}
            placeholder="Escolha a conta"
            density="compact"
            options={opcoesDeConta(sugeridas)}
            value={contaId}
            onChange={(evento) => onEscolherConta(evento.target.value)}
          />
        ) : null}

        {texto.rotuloCriar ? (
          <div className={styles.recuperacaoAcoes}>
            <Button
              variant="secondary"
              size="sm"
              onClick={onCriarConta}
              iconStart={<PlusIcon size={16} />}
            >
              {texto.rotuloCriar}
            </Button>
          </div>
        ) : null}

        <p className={styles.recuperacaoDica}>{texto.reenviar}</p>
      </div>
    </Alert>
  )
}

/** A extensão é uma PISTA para decidir se o campo de senha aparece, nunca uma
 *  validação: quem decide o que o arquivo é são os *magic bytes*, no servidor.
 *  Errar aqui custa um campo a mais ou a menos na tela — e o caso "a menos" é
 *  coberto pelo `IMPORT_PASSWORD_REQUIRED`, que revela o campo de qualquer
 *  jeito. */
function ehZip(arquivo: File | null): boolean {
  return arquivo?.name.toLowerCase().endsWith('.zip') ?? false
}

type ResultadoDoErro = {
  erros: ErrosDoPasso1
  pediuSenha: boolean
  recuperacao: Recuperacao | null
}

/** Cada código de erro da importação leva a uma AÇÃO DIFERENTE desta tela — é
 *  essa correspondência, e nada mais, que justifica vários códigos em vez de um.
 *
 *  Por isso a tradução mora aqui e não no `messageForError` genérico: o texto
 *  de 422 ("confira os dados") não diz nada sobre o que fazer com um ZIP
 *  protegido por senha, e "dados inválidos" num arquivo que só precisa de senha
 *  manda a pessoa procurar defeito onde não há. */
function interpretarErro(error: unknown): ResultadoDoErro {
  const so = (erros: ErrosDoPasso1): ResultadoDoErro => ({
    erros,
    pediuSenha: false,
    recuperacao: null,
  })

  if (!(error instanceof ApiError)) {
    return so({ geral: FALHA_DE_ENVIO })
  }

  switch (error.code) {
    case 'IMPORT_PASSWORD_REQUIRED':
      return { erros: {}, pediuSenha: true, recuperacao: null }

    case 'IMPORT_PASSWORD_INVALID':
      return {
        erros: { senha: 'Senha incorreta. No C6, é o CPF do titular, só números.' },
        pediuSenha: true,
        recuperacao: null,
      }

    case 'IMPORT_FORMAT_UNKNOWN':
      return so({
        geral:
          'Não reconhecemos este arquivo. Ele precisa ser o CSV do Nubank ou o ZIP do C6, do jeito que o banco exportou — sem renomear e sem abrir e salvar de novo.',
      })

    case 'IMPORT_FORMAT_AMBIGUOUS':
      return so({
        geral:
          'Mais de um leitor reconheceu este arquivo, então nada foi lido — ler pelo leitor errado inverteria o sinal dos valores. Confira se a conta de destino é a certa e tente de novo.',
      })

    case 'IMPORT_TARGET_MISMATCH': {
      const recuperacao = recuperacaoDoMismatch(error.fields)
      // Sem um motivo legível (`unknown_document`, defesa), volta ao antigo:
      // manda trocar a conta, sem oferecer caminho que não dá para montar.
      if (!recuperacao) {
        return so({
          conta: 'Este arquivo não é desta conta. Escolha a conta a que ele pertence.',
        })
      }
      return { erros: {}, pediuSenha: false, recuperacao }
    }

    case 'IMPORT_FILE_REJECTED':
      return so({
        geral:
          'Não deu para aceitar este arquivo: ele estourou um dos limites de tamanho, de número de linhas ou de período. Tente exportar um período menor no banco e importar de novo.',
      })

    case 'PAYLOAD_TOO_LARGE':
      return so({ arquivo: 'Arquivo grande demais. O limite é 8 MB.' })

    case 'RATE_LIMITED':
      return so({
        geral: 'Muitas importações em pouco tempo. Aguarde alguns minutos e tente de novo.',
      })

    default:
      break
  }

  if (error.status === 413) return so({ arquivo: 'Arquivo grande demais. O limite é 8 MB.' })

  // Conta arquivada como destino chega como 422 VALIDATION_FAILED com
  // `fields.accountId`, seguindo o padrão do resto da API.
  if (error.invalidFields.includes('accountId')) {
    return so({ conta: 'Esta conta não pode receber importação. Escolha outra conta de destino.' })
  }
  if (error.status === 404) {
    return so({ conta: 'Esta conta não existe mais. Escolha outra conta de destino.' })
  }

  return so({ geral: FALHA_DE_ENVIO })
}

/** Lê o motivo estruturado que o backend mandou em `error.fields`. Motivo
 *  ausente ou desconhecido devolve `null` — a tela cai no genérico. */
function recuperacaoDoMismatch(fields: Readonly<Record<string, string>>): Recuperacao | null {
  const detectada = instituicaoConhecida(fields.detectedInstitution)
  switch (fields.reason) {
    case 'expected_credit_card':
      return { tipo: 'expected_credit_card', instituicao: detectada }
    case 'expected_bank_account':
      return { tipo: 'expected_bank_account', instituicao: detectada }
    case 'wrong_institution':
      return {
        tipo: 'wrong_institution',
        detectada,
        daConta: instituicaoConhecida(fields.accountInstitution),
      }
    default:
      return null
  }
}

/** Só instituição da allowlist do contrato vira `Institution`; qualquer outra
 *  coisa (valor novo no backend, texto inesperado) é tratada como desconhecida
 *  em vez de virar um rótulo cru na tela. */
function instituicaoConhecida(valor: string | undefined): Institution | null {
  return valor !== undefined && (INSTITUICOES as readonly string[]).includes(valor)
    ? (valor as Institution)
    : null
}

/** As contas que fazem sentido para o motivo, com a instituição detectada na
 *  frente — é o palpite mais provável de onde o arquivo pertence. */
function contasSugeridas(recuperacao: Recuperacao, contas: readonly Account[]): Account[] {
  const filtradas = contas.filter((conta) => {
    if (recuperacao.tipo === 'expected_credit_card') return conta.kind === 'credit_card'
    if (recuperacao.tipo === 'expected_bank_account') return conta.kind !== 'credit_card'
    // wrong_institution: a conta certa é a da instituição do ARQUIVO.
    return recuperacao.detectada === null || conta.institution === recuperacao.detectada
  })

  const alvo = instituicaoAlvo(recuperacao)
  if (alvo === null) return filtradas
  return [...filtradas].sort(
    (a, b) => Number(b.institution === alvo) - Number(a.institution === alvo),
  )
}

function instituicaoAlvo(recuperacao: Recuperacao): Institution | null {
  return recuperacao.tipo === 'wrong_institution' ? recuperacao.detectada : recuperacao.instituicao
}

/** Valores iniciais do `AccountDialog` a partir do motivo: fatura → cartão de
 *  crédito; extrato → conta corrente, ambos já na instituição detectada, com um
 *  nome sugerido ("Cartão Nubank"). `wrong_institution` não chega aqui — lá o
 *  problema é escolher a conta certa, não a falta de uma. */
function prefillDaRecuperacao(recuperacao: Recuperacao): AccountPrefill {
  if (recuperacao.tipo === 'expected_bank_account') {
    const inst = recuperacao.instituicao
    return {
      kind: 'checking',
      ...(inst ? { institution: inst, name: `Conta ${ROTULO_DA_INSTITUICAO[inst]}` } : {}),
    }
  }
  const inst = recuperacao.tipo === 'expected_credit_card' ? recuperacao.instituicao : null
  return {
    kind: 'credit_card',
    ...(inst ? { institution: inst, name: `Cartão ${ROTULO_DA_INSTITUICAO[inst]}` } : {}),
  }
}

type TextoDaRecuperacao = {
  titulo: string
  explicacao: string
  rotuloEscolha: string
  rotuloCriar?: string
  reenviar: string
}

/** Os textos em pt-BR de cada motivo. Ficam juntos para que se leiam como um
 *  conjunto — o tom é o mesmo do resto da importação: explica o porquê e diz o
 *  próximo passo, sem culpar quem escolheu a conta. */
function textoDaRecuperacao(recuperacao: Recuperacao): TextoDaRecuperacao {
  const reenviar = 'Depois de escolher ou criar a conta, toque em Analisar arquivo de novo.'
  const nome = (inst: Institution | null) => (inst ? ROTULO_DA_INSTITUICAO[inst] : null)

  switch (recuperacao.tipo) {
    case 'expected_credit_card': {
      const banco = nome(recuperacao.instituicao)
      return {
        titulo: 'Isto parece uma fatura de cartão.',
        explicacao: banco
          ? `Faturas vão para uma conta de cartão de crédito, não para a conta que você escolheu. Aponte a fatura do ${banco} para o cartão certo — ou crie um agora.`
          : 'Faturas vão para uma conta de cartão de crédito, não para a conta que você escolheu. Escolha o cartão certo — ou crie um agora.',
        rotuloEscolha: 'Conta de cartão',
        rotuloCriar: banco ? `Criar conta de cartão do ${banco}` : 'Criar conta de cartão',
        reenviar,
      }
    }

    case 'expected_bank_account': {
      const banco = nome(recuperacao.instituicao)
      return {
        titulo: 'Isto parece um extrato de conta.',
        explicacao: banco
          ? `Extratos vão para uma conta corrente, não para um cartão. Aponte o extrato do ${banco} para a conta certa — ou crie uma agora.`
          : 'Extratos vão para uma conta corrente, não para um cartão. Escolha a conta certa — ou crie uma agora.',
        rotuloEscolha: 'Conta corrente',
        rotuloCriar: banco ? `Criar conta do ${banco}` : 'Criar conta corrente',
        reenviar,
      }
    }

    case 'wrong_institution': {
      const doArquivo = nome(recuperacao.detectada)
      const daConta = nome(recuperacao.daConta)
      const explicacao =
        doArquivo && daConta
          ? `Este arquivo é do ${doArquivo}, e a conta que você escolheu é do ${daConta}. Escolha a conta do ${doArquivo}.`
          : doArquivo
            ? `Este arquivo é do ${doArquivo}, e a conta que você escolheu é de outra instituição. Escolha a conta do ${doArquivo}.`
            : 'Este arquivo é de uma instituição diferente da conta que você escolheu. Escolha a conta a que ele pertence.'
      return {
        titulo: 'O arquivo e a conta são de instituições diferentes.',
        explicacao,
        rotuloEscolha: 'Conta certa',
        reenviar,
      }
    }
  }
}

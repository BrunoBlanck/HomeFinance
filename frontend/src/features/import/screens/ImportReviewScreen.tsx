import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from '@tanstack/react-router'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { ApiError } from '@/api/client'
import type { ImportRow, StatementConfirmation } from '@/api/types'
import { Alert } from '@/components/Alert/Alert'
import { FormError } from '@/components/Alert/FormError'
import { Button } from '@/components/Button/Button'
import { Dialog } from '@/components/Dialog/Dialog'
import { Spinner } from '@/components/Spinner/Spinner'
import { contaPorId, contasQueryOptions } from '@/lib/accounts'
import { categoriasQueryOptions } from '@/lib/categories'
import { dataCurta, mesDaDataCivil } from '@/lib/civil'
import { isCategoriaRecusada, messageForError } from '@/lib/errors'
import { uuidValido } from '@/lib/id'
import { valorComSinal } from '@/lib/kind'
import type { TransferenciaPendente } from '@/lib/navigation'
import {
  confirmarImportacao,
  descartarImportacao,
  linhasDaImportacaoQueryOptions,
} from '../api/imports'
import { BlocoDecisao } from '../components/BlocoDecisao'
import { BlocoForaDetails } from '../components/BlocoForaDetails'
import { BlocoProntas } from '../components/BlocoProntas'
import { BlocoTransferencias } from '../components/BlocoTransferencias'
import type { ContextoDaRevisao } from '../components/CelulasDaRevisao'
import { erroDaFatura, FaturaFields } from '../components/FaturaFields'
import { ImportStepper } from '../components/ImportStepper'
import {
  acaoEfetiva,
  categoriaEfetiva,
  contarRevisao,
  type Escolha,
  type Escolhas,
  montarDecisoes,
  nadaMarcado,
  nuanceDaConfirmacao,
  rotuloDoConfirmar,
  transferenciasIncompletas,
} from '../decisoes'
import { BLOCO_DO_STATUS } from '../lexico'
import styles from './ImportReviewScreen.module.css'

/** Teto de decisões do contrato. Existe para o corpo caber no limite de 1 MiB:
 *  com 10.000 `rowId` ele estouraria. */
const MAX_DECISOES = 2000

/** Passo 2 da importação — **revisar o que vai entrar**.
 *
 *  É a tela onde a pessoa impede que uma linha duplicada entre, e ela está
 *  organizada por **trabalho a fazer**, não por status do dado. Os nove status
 *  da conciliação não são nove coisas: são quatro situações, num gradiente de
 *  trabalho.
 *
 *  1. *Precisam da sua decisão* — perguntas abertas; `<select>` com palavras;
 *  2. *Transferências detectadas* — perguntas **com resposta proposta**; o
 *     mesmo `<select>`, com a contraparte sugerida no texto da opção;
 *  3. *Prontas para importar* — conferência; checkbox, categoria já sugerida;
 *  4. *Ficam de fora* — auditoria; `<details>` fechado, **sem controle nenhum**.
 *
 *  **Nenhuma marcação de duplicata usa cor.** A distinção vem de posição,
 *  palavra do grupo, frase de evidência na linha e forma do controle — quatro
 *  portadores que sobrevivem ao daltonismo, ao `filter: grayscale(1)` e à
 *  impressão em preto e branco. A pontuação da correspondência ("88%") é
 *  texto. As únicas cores cromáticas da tela são os contadores dos blocos 1 e
 *  2 (`--warning`, "algo esperando decisão sua") e o valor das linhas, que é
 *  dinheiro, não status.
 *
 *  O default de cada linha é do **servidor** (`defaultAction`/`allowedActions`).
 *  A tela não reimplementa a tabela de status: uma cópia dela aqui divergiria no
 *  primeiro ajuste do backend, em silêncio, e passaria a prometer uma coisa
 *  enquanto o commit faz outra. */
export function ImportReviewScreen() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const parametros = useParams({ strict: false })

  // Foco no <h1> por REF DE CALLBACK, e não por `useEffect` de montagem.
  //
  // Esta tela renderiza duas árvores diferentes — o esqueleto do carregamento e
  // a revisão — e o `h1` da primeira é DESMONTADO quando os dados chegam. Um
  // efeito com lista vazia focaria só o primeiro, e o foco cairia no <body>
  // justamente quando a tela ficou útil. A ref de callback é chamada toda vez
  // que um `h1` monta, que é exatamente o momento certo, nas duas fases.
  const focarTitulo = useCallback((titulo: HTMLHeadingElement | null) => {
    titulo?.focus()
  }, [])
  const importId = typeof parametros.importId === 'string' ? parametros.importId : ''

  const [escolhas, setEscolhas] = useState<Escolhas>({})
  const [fatura, setFatura] = useState<StatementConfirmation | null>(null)
  const [erroDeEnvio, setErroDeEnvio] = useState<string | undefined>(undefined)
  const [cobrandoMarcacao, setCobrandoMarcacao] = useState(false)
  const [cobrandoDestino, setCobrandoDestino] = useState(false)
  const [cancelando, setCancelando] = useState(false)
  const [colidiu, setColidiu] = useState(false)

  const idValido = uuidValido(importId)
  const revisao = useInfiniteQuery({
    ...linhasDaImportacaoQueryOptions(importId),
    enabled: idValido,
  })
  const contas = useQuery(contasQueryOptions(true))
  const categorias = useQuery(categoriasQueryOptions(false))

  useEffect(() => {
    document.title = 'Revisar importação · HomeFinance'
  }, [])

  const { hasNextPage, isFetchingNextPage, fetchNextPage } = revisao

  // A revisão precisa das linhas TODAS: os três blocos, as contagens e o corpo
  // do confirm são sobre o lote inteiro. Confirmar com metade carregada
  // mandaria as decisões de metade e deixaria a outra metade no default sem a
  // pessoa nunca ter visto.
  useEffect(() => {
    if (hasNextPage && !isFetchingNextPage) void fetchNextPage()
  }, [hasNextPage, isFetchingNextPage, fetchNextPage])

  const lote = revisao.data?.pages[0]?.batch
  const linhas = useMemo(
    () => revisao.data?.pages.flatMap((pagina) => pagina.items) ?? [],
    [revisao.data],
  )

  // Fatura: a sugestão do servidor entra uma vez, como valor inicial. Reaplicá-la
  // a cada render apagaria o que a pessoa acabou de escolher.
  useEffect(() => {
    if (fatura !== null) return
    const sugestao = lote?.statementSuggestion
    if (!sugestao) return
    setFatura({
      competenceMonth: sugestao.competenceMonth,
      closingDate: sugestao.closingDate,
      dueDate: sugestao.dueDate,
    })
  }, [lote, fatura])

  // Lote inexistente, expirado ou de outra casa: 404 igual para os três, e a
  // tela volta ao começo em vez de mostrar uma revisão vazia.
  useEffect(() => {
    const inexistente =
      !idValido || (revisao.error instanceof ApiError && revisao.error.status === 404)
    if (!inexistente) return
    void navigate({
      to: '/importar',
      replace: true,
      state: {
        flash: {
          tone: 'warning',
          message: 'Esta análise não existe mais. Envie o arquivo de novo.',
        },
      },
    })
  }, [idValido, revisao.error, navigate])

  const porBloco = separarPorBloco(linhas)
  const contagem = contarRevisao(linhas, escolhas)
  const semDestino = transferenciasIncompletas(linhas, escolhas)
  const ehFatura = lote?.docKind === 'card_statement'
  const erroDeFatura = fatura ? erroDaFatura(fatura) : undefined
  const contaDoLote = contaPorId(contas.data?.items ?? [], lote?.accountId)

  // O mês afetado é o do ARQUIVO, nunca o corrente: quem importa o extrato de
  // agosto em setembro quer o link levando a agosto. Em fatura, o mês é a
  // competência que a pessoa confirmou — é lá que as linhas aparecem.
  const mesDoLote =
    (ehFatura && fatura ? fatura.competenceMonth : undefined) ??
    (lote ? (mesDaDataCivil(lote.maxDate) ?? '') : '')

  // A categoria efetiva conta a sugestão do servidor: uma linha que entra com
  // "Alimentação" sugerida NÃO entra sem categoria.
  const semCategoria = linhas.filter(
    (linha) =>
      acaoEfetiva(linha, escolhas) === 'import' && categoriaEfetiva(linha, escolhas) === null,
  ).length

  const faturaIgnorada = acharFaturaIgnorada(linhas, escolhas, {
    id: lote?.accountId ?? '',
    nome: contaDoLote?.name ?? 'conta do lote',
  })

  const confirmar = useMutation({
    mutationFn: () => {
      const decisoes = montarDecisoes(linhas, escolhas)
      return confirmarImportacao(importId, {
        decisions: decisoes,
        ...(ehFatura && fatura ? { statement: fatura } : {}),
      })
    },
    onSuccess: async (resultado) => {
      // 200 NÃO quer dizer "gravou". Quando o índice único recusa o lote — os
      // dois moradores da casa confirmando o mesmo arquivo ao mesmo tempo —, o
      // servidor **desfaz a transação inteira** e responde 200 com
      // `status: "pending"`, `imported: 0` e `blocked: N`. Nada foi escrito, e
      // as linhas de staging continuam vivas pelas 24 h do lote.
      //
      // Seguir para o passo 3 aqui seria mentir duas vezes: o título diria
      // "Importação concluída" sobre um lote que não concluiu, e o estado vazio
      // mandaria a pessoa enviar o arquivo de novo enquanto o lote dela ainda
      // está ali, válido e pendente. O caminho certo é ficar, recarregar a
      // revisão (o que o outro lote gravou agora aparece como duplicata) e
      // confirmar de novo.
      if (resultado.status !== 'committed') {
        setColidiu(true)
        await queryClient.invalidateQueries({
          queryKey: linhasDaImportacaoQueryOptions(importId).queryKey,
        })
        return
      }

      // Os lançamentos acabaram de existir e os saldos mudaram: as duas telas
      // que os mostram precisam parar de servir o cache anterior.
      await queryClient.invalidateQueries({ queryKey: ['transactions'] })
      await queryClient.invalidateQueries({ queryKey: ['accounts'] })
      void navigate({
        to: '/importar/$importId/resultado',
        params: { importId },
        replace: true,
        state: {
          resumoDaImportacao: {
            resultado,
            mes: mesDoLote,
            semCategoria: semCategoria,
            ...(faturaIgnorada ? { faturaIgnorada } : {}),
          },
        },
      })
    },
    onError: (error) => {
      setErroDeEnvio(messageForError(error))
      // 422 em `fields.categoryId` (spec 0005 §13): alguma decisão (ou o
      // `defaultCategoryId`) aponta para um grupo que ganhou subcategoria
      // depois que esta tela leu a árvore. O confirm é tudo-ou-nada — NADA
      // foi gravado, e o lote continua vivo —, então a saída é recarregar as
      // categorias, para o grupo sumir dos seletores das linhas, corrigir a
      // linha e confirmar de novo. O erro do passo já diz isso; sem a
      // recarga, a lista ofereceria o mesmo grupo recusado.
      if (isCategoriaRecusada(error)) {
        void queryClient.invalidateQueries({ queryKey: ['categories'] })
      }
    },
  })

  const descartar = useMutation({
    mutationFn: () => descartarImportacao(importId),
    // O descarte apaga as linhas de staging fisicamente — é o que honra a
    // promessa feita no rodapé do passo 1. Falhar não pode prender a pessoa
    // nesta tela: ela sai de qualquer jeito, e o janitor limpa em 24 h.
    onSettled: () => {
      setCancelando(false)
      void navigate({ to: '/importar', replace: true })
    },
  })

  function escolher(rowId: string, escolha: Escolha) {
    setEscolhas((anteriores) => ({ ...anteriores, [rowId]: escolha }))
    setCobrandoMarcacao(false)
    setCobrandoDestino(false)
  }

  function aplicarATodas(alvo: readonly ImportRow[], acao: 'import' | 'skip') {
    setEscolhas((anteriores) => {
      const proximas = { ...anteriores }
      for (const linha of alvo) {
        const atual = proximas[linha.id]
        proximas[linha.id] = {
          acao,
          // `null` ("sem categoria apesar da sugestão") também é uma escolha
          // que sobrevive ao marcar/desmarcar — só `undefined` é "não mexi".
          ...(atual?.categoriaId !== undefined ? { categoriaId: atual.categoriaId } : {}),
        }
      }
      return proximas
    })
  }

  /** "Aceitar as N transferências sugeridas": `transfer` com a contraparte
   *  sugerida (o servidor a usa) em TODAS as `transferencia_interna` do bloco
   *  — e em nenhuma outra linha. `transferencia_ja_registrada` já resolve
   *  sozinha por `link`, e as demais linhas nem aceitam `transfer`. */
  function aceitarTransferencias(aceitar: boolean) {
    setEscolhas((anteriores) => {
      const proximas = { ...anteriores }
      for (const linha of porBloco.transferencias) {
        if (linha.status !== 'transferencia_interna') continue
        if (!linha.allowedActions.includes('transfer')) continue
        proximas[linha.id] = { acao: aceitar ? 'transfer' : 'skip' }
      }
      return proximas
    })
    setCobrandoDestino(false)
  }

  function submeter() {
    setErroDeEnvio(undefined)
    // O aviso de colisão é sobre a TENTATIVA anterior: uma nova tentativa o
    // apaga, e ele volta se colidir de novo — remontar é o que leva o foco até
    // ele outra vez.
    setColidiu(false)

    if (nadaMarcado(contagem)) {
      setCobrandoMarcacao(true)
      return
    }
    if (semDestino.length > 0) {
      setCobrandoDestino(true)
      return
    }
    if (erroDeFatura) return
    if (montarDecisoes(linhas, escolhas).length > MAX_DECISOES) {
      setErroDeEnvio(
        `São mais de ${MAX_DECISOES} decisões de uma vez, e o servidor não aceita um lote desse tamanho. Importe um período menor.`,
      )
      return
    }

    confirmar.mutate()
  }

  if (revisao.isPending || !lote) {
    return (
      <div className={styles.pagina}>
        <h1 className={styles.titulo} ref={focarTitulo} tabIndex={-1}>
          Revisar o que vai entrar
        </h1>
        <ImportStepper atual="revisar" />
        {revisao.isError ? (
          <Alert
            tone="error"
            title="Não foi possível carregar a revisão."
            action={
              <Button onClick={() => void revisao.refetch()} loading={revisao.isFetching}>
                Tentar de novo
              </Button>
            }
          >
            {messageForError(revisao.error)}
          </Alert>
        ) : (
          <p className={styles.carregando} role="status">
            <Spinner size="sm" /> Carregando as linhas do arquivo…
          </p>
        )}
      </div>
    )
  }

  const nuance = nuanceDaConfirmacao(contagem)

  // O que os blocos com controle têm em comum: as escolhas e o cache de contas
  // e categorias — as células escrevem NOMES, nunca ids.
  const contexto: ContextoDaRevisao = {
    escolhas,
    contas: contas.data?.items ?? [],
    contaDoLoteId: lote.accountId,
    categorias: categorias.data,
    onEscolher: escolher,
  }
  const faltandoDestino = cobrandoDestino
    ? new Set(semDestino.map((linha) => linha.id))
    : new Set<string>()

  return (
    <div className={styles.pagina}>
      <div>
        <h1 className={styles.titulo} ref={focarTitulo} tabIndex={-1}>
          Revisar o que vai entrar
        </h1>
        {/* Visível E `role="status"` — sem duplicata `sr-only`. Duas cópias da
            mesma frase fariam o leitor de tela ler tudo duas vezes. */}
        <p className={styles.contexto} role="status">
          {lote.rowCount} {lote.rowCount === 1 ? 'linha lida' : 'linhas lidas'} ·{' '}
          {contaDoLote?.name ?? 'conta do lote'} · {dataCurta(lote.minDate)} a{' '}
          {dataCurta(lote.maxDate)}
        </p>
        <p className={styles.contagemDosBlocos}>
          {porBloco.decisao.length} precisam da sua decisão · {porBloco.transferencias.length}{' '}
          transferências detectadas · {porBloco.prontas.length} prontas para importar ·{' '}
          {porBloco.fora.length} ficam de fora.
        </p>
      </div>

      <ImportStepper atual="revisar" />

      {/* A confirmação voltou 200, mas o lote foi DESFEITO pelo índice único.
          O foco vem para cá porque a pessoa clicou em "Importar N lançamentos"
          e a tela não saiu do lugar — sem isso, quem usa leitor de tela ou
          teclado ficaria sem saber o que aconteceu com o clique. */}
      {colidiu ? (
        <Alert tone="warning" title="Outra importação gravou estas linhas primeiro" autoFocus>
          Nada foi gravado por esta confirmação e o seu arquivo continua aqui. A revisão foi
          recarregada: confira o que mudou e confirme de novo.
        </Alert>
      ) : null}

      {/* Aviso, nunca bloqueio: um byte a mais no arquivo já mudaria o hash, e
          quem reexporta de propósito para pegar três linhas novas não pode ser
          travado. As repetidas aparecem abaixo como "Já importada". */}
      {lote.sameContentImportedAt ? (
        <Alert tone="warning" title="Este mesmo conteúdo já foi importado">
          O arquivo com este conteúdo entrou em {dataHoraCurta(lote.sameContentImportedAt)}. As
          linhas que já existem aparecem abaixo como "Já importada" e não entram de novo.
        </Alert>
      ) : null}

      {ehFatura && fatura ? (
        <FaturaFields
          nomeDaConta={contaDoLote?.name ?? 'cartão'}
          valor={fatura}
          onChange={setFatura}
          {...(erroDeFatura ? { erro: erroDeFatura } : {})}
        />
      ) : null}

      <BlocoDecisao
        linhas={porBloco.decisao}
        contexto={contexto}
        onDesfazer={() => aplicarATodas(porBloco.decisao, 'skip')}
        faltandoDestino={faltandoDestino}
      />

      <BlocoTransferencias
        linhas={porBloco.transferencias}
        contexto={contexto}
        onAceitarTodas={() => aceitarTransferencias(true)}
        onDesfazerAceite={() => aceitarTransferencias(false)}
        faltandoDestino={faltandoDestino}
      />

      <BlocoProntas
        linhas={porBloco.prontas}
        contexto={contexto}
        onMarcarTodas={() => aplicarATodas(porBloco.prontas, 'import')}
        onDesmarcarTodas={() => aplicarATodas(porBloco.prontas, 'skip')}
      />

      <BlocoForaDetails linhas={porBloco.fora} />

      {erroDeEnvio ? <FormError id="confirmar-erro">{erroDeEnvio}</FormError> : null}
      {cobrandoMarcacao ? (
        <FormError id="nada-marcado">Marque ao menos uma linha, ou cancele a importação.</FormError>
      ) : null}
      {cobrandoDestino ? (
        <FormError id="sem-destino">
          Escolha a conta de destino de cada linha marcada como transferência.
        </FormError>
      ) : null}

      {/* Grudada no rodapé por BORDA, nunca por sombra: não é camada flutuante,
          é o rodapé do documento. */}
      <div className={styles.barra}>
        <output className={styles.nuance} aria-live="polite">
          {nuance}
        </output>
        <div className={styles.acoesDaBarra}>
          <Button variant="quiet" onClick={() => setCancelando(true)}>
            Cancelar importação
          </Button>
          {/* NUNCA `disabled`: quem chega no botão pelo teclado precisa
              descobrir POR QUE não pode prosseguir, e um botão apagado não
              conta nada a ninguém. O rótulo muda e o clique explica. */}
          <Button
            variant="primary"
            onClick={submeter}
            loading={confirmar.isPending}
            aria-disabled={nadaMarcado(contagem) ? true : undefined}
          >
            {rotuloDoConfirmar(contagem)}
          </Button>
        </div>
      </div>

      <Dialog
        open={cancelando}
        onClose={() => setCancelando(false)}
        title="Cancelar esta importação?"
        footer={
          <>
            <Button variant="quiet" onClick={() => setCancelando(false)}>
              Voltar para a revisão
            </Button>
            <Button
              variant="danger"
              onClick={() => descartar.mutate()}
              loading={descartar.isPending}
            >
              Descartar o arquivo
            </Button>
          </>
        }
      >
        <p className={styles.corpoDoDialogo}>
          O arquivo é descartado e nada é gravado. Suas decisões desta tela se perdem.
        </p>
      </Dialog>
    </div>
  )
}

type LinhasPorBloco = {
  decisao: ImportRow[]
  transferencias: ImportRow[]
  prontas: ImportRow[]
  fora: ImportRow[]
}

/** Os nove status viram quatro blocos de trabalho. Uma passagem só pelas
 *  linhas: num lote de 10.000, quatro `filter` seriam quatro varreduras. */
function separarPorBloco(linhas: readonly ImportRow[]): LinhasPorBloco {
  const porBloco: LinhasPorBloco = { decisao: [], transferencias: [], prontas: [], fora: [] }
  for (const linha of linhas) {
    porBloco[BLOCO_DO_STATUS[linha.status]].push(linha)
  }
  return porBloco
}

/** O pagamento de fatura que a pessoa deixou de fora, se houve.
 *
 *  Vai no state da navegação para o passo 3, que avisa a consequência: enquanto
 *  esse pagamento não for registrado como transferência, a dívida do cartão não
 *  volta a zero — o dinheiro saiu da conta e o cartão continua devendo. */
function acharFaturaIgnorada(
  linhas: readonly ImportRow[],
  escolhas: Escolhas,
  conta: { id: string; nome: string },
): TransferenciaPendente | undefined {
  const ignorada = linhas.find(
    (linha) => linha.status === 'pagamento_de_fatura' && acaoEfetiva(linha, escolhas) === 'skip',
  )
  if (!ignorada || ignorada.amountCents === null || !ignorada.occurredOn || !ignorada.kind) {
    return undefined
  }
  return {
    valorCents: Math.abs(valorComSinal(ignorada.kind, ignorada.amountCents)),
    data: ignorada.occurredOn,
    contaOrigemId: conta.id,
    contaOrigemNome: conta.nome,
  }
}

/** Um `date-time` do servidor é um INSTANTE de verdade — diferente de uma data
 *  civil, ele pode e deve passar pelo construtor de `Date`. */
function dataHoraCurta(iso: string): string {
  return new Intl.DateTimeFormat('pt-BR', { dateStyle: 'short' }).format(new Date(iso))
}

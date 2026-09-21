import { type DragEvent, type Ref, useId, useRef } from 'react'
import { Button } from '../Button/Button'
import { FieldShell } from '../FieldShell/FieldShell'
import styles from './FileField.module.css'

type FileFieldProps = {
  label: string
  /** Lista de extensões para o seletor do sistema — `".csv,.zip"`. Filtro de
   *  conveniência, nunca validação: quem decide o que o arquivo é são os
   *  *magic bytes*, no servidor. */
  accept: string
  hint?: string | undefined
  error?: string | undefined
  file?: File | null | undefined
  onSelect: (file: File | null) => void
  ref?: Ref<HTMLInputElement> | undefined
}

/** Campo de arquivo.
 *
 *  O `<input type="file">` é o elemento REAL, visível e rotulado pelo `<label
 *  for>` do `FieldShell` — nunca um `<div>` clicável com o input escondido
 *  atrás (docs/DESIGN.md). O padrão do `<div>` custa caro e o preço é sempre o
 *  mesmo: perde o foco por teclado, perde o papel "botão" no leitor de tela,
 *  perde o atalho do sistema para abrir o seletor e, no celular, perde a
 *  integração com câmera e arquivos recentes.
 *
 *  O que damos ao input, e só isso: o `::file-selector-button` ganha o desenho
 *  de botão secundário do projeto, e o TEXTO nativo ao lado dele (o
 *  "Nenhum arquivo selecionado" do navegador) é neutralizado por `font-size: 0`
 *  para que o nome do arquivo saia na nossa própria linha — onde ele pode
 *  truncar com reticências e carregar `title`. Um nome de 120 caracteres no
 *  texto nativo estoura o painel e não há CSS que o corte.
 *
 *  Arrastar-e-soltar é **melhoria**, nunca o caminho principal: o drop grava em
 *  `input.files` via `DataTransfer`, então o input continua sendo a fonte da
 *  verdade e o formulário não precisa saber se o arquivo chegou pelo seletor ou
 *  pelo arraste. */
export function FileField({ label, accept, hint, error, file, onSelect, ref }: FileFieldProps) {
  const reactId = useId()
  const id = `${reactId}-file`
  const messageId = `${reactId}-message`
  const interno = useRef<HTMLInputElement>(null)
  const zonaRef = useRef<HTMLDivElement>(null)

  function referenciar(node: HTMLInputElement | null) {
    interno.current = node
    if (typeof ref === 'function') ref(node)
    else if (ref) ref.current = node
  }

  function marcarArraste(ativo: boolean) {
    zonaRef.current?.setAttribute('data-dragover', String(ativo))
  }

  function aoArrastar(evento: DragEvent<HTMLDivElement>) {
    // Sem o preventDefault o navegador ABRE o arquivo, trocando a página do app
    // pelo CSV cru — e o trabalho da tela se perde.
    if (!evento.dataTransfer.types.includes('Files')) return
    evento.preventDefault()
    evento.dataTransfer.dropEffect = 'copy'
    marcarArraste(true)
  }

  function aoSoltar(evento: DragEvent<HTMLDivElement>) {
    evento.preventDefault()
    marcarArraste(false)

    const solto = evento.dataTransfer.files.item(0)
    if (!solto) return

    // Um arquivo por vez: o contrato aceita um `file` só, e aceitar a pilha
    // inteira em silêncio importaria o primeiro e descartaria o resto sem
    // dizer nada.
    const campo = interno.current
    if (campo) {
      try {
        const transferencia = new DataTransfer()
        transferencia.items.add(solto)
        campo.files = transferencia.files
      } catch {
        // `DataTransfer` não existe em todo ambiente (jsdom, por exemplo). O
        // arraste é melhoria: sem ele o seletor continua funcionando, e o
        // estado do formulário ainda recebe o arquivo pelo onSelect abaixo.
      }
    }
    onSelect(solto)
  }

  function remover() {
    const campo = interno.current
    // `value = ''` é o único jeito de esvaziar um input de arquivo. Sem isso,
    // escolher o MESMO arquivo de novo não dispara `change` — o navegador vê
    // que nada mudou — e a tela ficaria travada sem explicação.
    if (campo) campo.value = ''
    onSelect(null)
    campo?.focus()
  }

  return (
    <FieldShell id={id} messageId={messageId} label={label} hint={hint} error={error}>
      {/* biome-ignore lint/a11y/noStaticElementInteractions: a zona só escuta ARRASTE, que não existe para teclado nem para leitor de tela. Ela não tem onClick e não é o caminho de escolher arquivo — quem faz isso é o <input type="file"> real que mora dentro dela, com foco, rótulo e o papel de botão do navegador. Dar role/tabIndex a este div criaria um segundo alvo de foco que não faz nada. */}
      <div
        ref={zonaRef}
        className={styles.zona}
        data-preenchido={file ? 'true' : undefined}
        onDragOver={aoArrastar}
        onDragEnter={aoArrastar}
        onDragLeave={() => marcarArraste(false)}
        onDrop={aoSoltar}
      >
        <div className={styles.linha}>
          <input
            ref={referenciar}
            id={id}
            type="file"
            accept={accept}
            className={styles.input}
            aria-describedby={messageId}
            aria-invalid={error ? true : undefined}
            onChange={(evento) => onSelect(evento.target.files?.item(0) ?? null)}
          />

          {file ? (
            <>
              <span className={styles.nome} title={file.name}>
                {file.name}
              </span>
              <span className={styles.tamanho}>{formatarTamanho(file.size)}</span>
              <Button variant="quiet" size="sm" onClick={remover}>
                Remover
              </Button>
            </>
          ) : (
            <span className={styles.vazio}>nenhum arquivo selecionado</span>
          )}
        </div>

        {file ? null : <p className={styles.arraste}>ou arraste o arquivo para cá</p>}
      </div>
    </FieldShell>
  )
}

const UM_KB = 1024
const UM_MB = UM_KB * UM_KB
const numero = new Intl.NumberFormat('pt-BR', { maximumFractionDigits: 1 })

/** "18 KB", "1,4 MB". Existe para a pessoa reconhecer o arquivo que escolheu —
 *  um extrato de 2 KB quando ela esperava 200 KB é o sinal de que baixou a
 *  página de login do banco em vez do CSV. */
function formatarTamanho(bytes: number): string {
  if (bytes < UM_KB) return `${numero.format(bytes)} bytes`
  if (bytes < UM_MB) return `${numero.format(bytes / UM_KB)} KB`
  return `${numero.format(bytes / UM_MB)} MB`
}
